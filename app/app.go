// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package app is the Bubble Tea host. It owns the terminal, the nthtml viewer,
// navigation policy and history. It knows nothing about JavaScript beyond the
// loop's messages.
package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"

	"nimbleterminal.dev/ntbrowser/cookies"
	"nimbleterminal.dev/ntbrowser/diagnostics"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
)

// NavMode is how link navigation is admitted: the profile's navigate gate.
type NavMode = profile.Navigate

const (
	NavOff       = profile.NavOff
	NavFree      = profile.NavFree      // a reader-activated link may open any http(s) origin, like a browser
	NavAllowlist = profile.NavAllowlist // confined to the current origin plus the profile's hosts
	NavConfirm   = profile.NavConfirm   // leaving the current origin asks in the status line first
)

type pageLoadedMsg struct {
	html string
	base *url.URL
	seq  uint64 // navigation sequence; only the latest navigation may land
	hist int    // history index to make current, or pushNew
	err  error

	// What navigation found, when it is not a page: a line for the status
	// (the current page stays), or a picture to show in the page's place.
	finalURL string
	line     string
	img      image.Image
	player   *animation.Player
	video    string // a downloaded video's name in the media directory
}

// pushNew means "truncate forward history and append" (a link, the start
// page, a confirmed prompt); any other value is Back/Forward/Reload.
const pushNew = -1

type shutdownReadyMsg struct{}

const shutdownGrace = 150 * time.Millisecond

type Model struct {
	debug             debugPanel
	glynx             bool
	scriptPermissions []loop.ScriptPermissionMsg

	loop              *loop.Loop
	viewer            *nthtml.Model
	cssPainted        bool
	viewerInitialized bool
	layoutWidth       int
	profile           profile.Profile  // frozen at launch
	policy            netpolicy.Policy // template derived from it; Origin is set per navigation
	nav               NavMode
	cols              int
	rows              int
	gen               uint64
	lastFrame         uint64
	frameRegions      []image.Rectangle
	embeddedFocus     bool
	chunk             loop.ChunkInfo
	chunkFailed       bool
	title             string
	addressError      string
	addressOpen       bool
	address           textinput.Model
	status            string
	closing           bool
	start             string

	current        *url.URL
	history        []string
	hIndex         int // index of current in history; -1 before the first load
	pendingNav     string
	pendingScript  *scriptPlan
	scriptNavCount int
	documentNav    uint64
	documentSeq    uint64

	// resetView marks the next frame as the first of a new page: scroll is
	// not carried over. pressCell/pressed turn a press+release on the same
	// cell into an element click (browser semantics, and the same rule nthtml
	// uses before it emits LinkMsg).
	resetView bool
	pressCell image.Point
	pressGen  uint64
	pressed   bool
	// navSeq counts navigations started; a pageLoadedMsg carrying an older
	// sequence is a slower, superseded load and is dropped. History is only
	// touched when a load succeeds (decision 20).
	navSeq uint64

	// Forms (forms.go): the input mode, the last focus report, and the
	// editor and chooser state.
	mode  inputMode
	focus loop.FocusMsg
	ring  string     // the element the viewer's focus ring is on ("" = none)
	media *mediaView // a picture shown in the page's place (content.go)

	// Video (player.go): the helper, the media directory, the player, and
	// the navigation that may be downloading.
	bridge      *MediaBridge
	mediaDir    string
	player      *player
	navCancel   context.CancelFunc
	navURL      string
	downloading atomic.Bool
	notEnforced bool
	input       textinput.Model
	area        textarea.Model
	choice      int
}

// viewerOptions tunes the nthtml viewer for a page that re-flows often and
// scrolls by keypress (perf analysis 2026-10-27, HN front page). StableID
// keeps the Kitty image id across frames while geometry is unchanged, so a
// re-flow or scroll step changes pixels but not the placeholder grid and
// Bubble Tea has nothing to repaint; without it every keypress redrew all
// cells. Zlib frames encode about three times faster than PNG and are
// cheaper for the terminal to decode. ReflowTypeset stays off (decision 19).
var viewerOptions = nthtml.ModelOptions{
	HostPainted: true,
	Picture: ntpicture.Options{
		StableID: true,
		Render:   ntpicture.Config{KittyFormat: ntpicture.KittyFormatZlib},
	},
}

func New(l *loop.Loop, p profile.Profile, start string) *Model {
	return &Model{loop: l, profile: p, policy: PolicyFor(p), nav: p.Navigate, cols: 80, rows: 24, start: start, hIndex: -1}
}

// PolicyFor is the network policy template a profile implies.
func PolicyFor(p profile.Profile) netpolicy.Policy {
	return netpolicy.Policy{AllowHosts: append([]string(nil), p.Hosts...), AllowPrivate: p.PrivateNetwork}
}

func (m *Model) Init() tea.Cmd {
	if m.start == "" {
		return m.openAddress()
	}
	return m.navigate(m.start, pushNew, true)
}

// navigate loads an absolute URL and hands the HTML to the loop.
//
// Top-level navigation and a page's fetch() are two policies (decision 11):
// navigation under -nav free, or after a confirm, may go to any http(s) host
// and follow redirects across scheme and host (anyOrigin); under -nav
// allowlist, and for a same-origin link in confirm mode, redirects must stay
// inside the current origin plus -allow. Both still refuse credentials,
// local names, forbidden literal and resolved addresses, cookies and bodies
// over 1 MiB, because they share netpolicy's dialer.
//
// file: is accepted only for the start page the host asked for; links can
// never produce it because document.ResolveURL rejects local schemes. The
// page Location requests use the additional script-navigation gate in
// location.go; reader navigation continues through NavMode in Update.
func (m *Model) navigate(href string, hist int, anyOrigin bool, originOnly ...bool) tea.Cmd {
	m.pendingNav = ""
	m.pendingScript = nil
	m.scriptPermissions = nil
	m.navSeq++
	seq := m.navSeq
	// file: is allowed for the page the user asked for on the command line,
	// whenever it is (re)visited: first load, Reload, Back. Captured on the
	// Update goroutine; the Cmd runs elsewhere.
	startKey := m.start
	if su, err := url.Parse(m.start); err == nil {
		startKey = su.String()
	}
	firstPage := m.hIndex < 0
	policy := m.policy
	if len(originOnly) > 0 && originOnly[0] {
		policy.AllowHosts = nil
	}
	policy.Origin, policy.AnyOrigin, policy.MaxBody = m.current, anyOrigin, 1<<20
	policy.Timeout = navTimeout
	gates := m.profile
	if m.navCancel != nil {
		m.navCancel() // a newer navigation wins; an unfinished download stops
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.navCancel, m.navURL = cancel, href
	var video *videoTarget
	if m.bridge != nil {
		video = &videoTarget{dir: m.mediaDir, cap: m.profile.MaxMediaBytes, active: &m.downloading, dead: m.bridge.dead.Load(), progress: func(n int64) { m.bridge.send(videoProgressMsg{n}) }}
	}
	return func() tea.Msg {
		u, err := url.Parse(href)
		if err != nil {
			return pageLoadedMsg{seq: seq, err: err}
		}
		switch u.Scheme {
		case "file":
			if !firstPage && u.String() != startKey {
				return pageLoadedMsg{seq: seq, err: fmt.Errorf("file: navigation is only allowed for the start page")}
			}
			data, err := readLocal(u.Path)
			return pageLoadedMsg{html: data, base: u, seq: seq, hist: hist, err: err}
		case "http", "https":
		default:
			return pageLoadedMsg{seq: seq, err: fmt.Errorf("refusing to open %s", u.Scheme)}
		}
		if policy.Origin == nil {
			policy.Origin = u // first page: nothing to be same-origin with but itself
		}
		f := netpolicy.New(policy)
		defer cancel()
		msg := load(ctx, cancel, f, u.String(), gates, video, pageLoadedMsg{seq: seq, hist: hist})
		msg.base, _ = url.Parse(msg.finalURL)
		return msg
	}
}

// anyOriginFor is the navigation policy for a URL the user already admitted
// (Back, Forward, Reload): only -nav allowlist keeps redirects confined.
func (m *Model) anyOriginFor() bool { return m.nav != NavAllowlist }

// admit applies the NavMode to a link the page did not prevent, or to a form
// submission (same gate, its own confirmation wording).
func (m *Model) admit(href string, form bool, script ...bool) tea.Cmd {
	if len(script) > 0 && script[0] {
		if m.pendingNav != "" {
			return nil
		}
	} else {
		m.scriptNavCount = 0
		m.pendingNav = ""
		m.pendingScript = nil
	}
	if m.downloading.Load() && href == m.navURL {
		return nil // already on its way; anything else replaces the download
	}
	u, err := url.Parse(href)
	if err != nil {
		m.status = clean("bad link: " + err.Error())
		return nil
	}
	sameOrigin := m.current != nil && m.current.Scheme == u.Scheme && strings.EqualFold(m.current.Host, u.Host)
	switch m.nav {
	case NavFree:
	case NavAllowlist:
		p := m.policy
		p.Origin = m.current
		if err := netpolicy.New(p).Allowed(u); err != nil {
			m.status = clean("blocked: " + err.Error())
			return nil
		}
	default:
		m.status = "blocked: navigation is off in this profile"
		return nil
	case NavConfirm:
		if !sameOrigin {
			m.pendingNav = href
			if form {
				m.status = clean("Submit form to " + u.Host + "? Enter to send · Esc to stay") // the host, not the values in the URL
			} else {
				m.status = clean("Open " + href + "? Enter to open · Esc to stay")
			}
			return nil
		}
		return m.navigate(href, pushNew, false) // same-origin: a redirect off-origin is refused, not silently followed
	}
	return m.navigate(href, pushNew, m.nav == NavFree)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer m.syncViewport()
	if m.closing {
		if _, ok := msg.(shutdownReadyMsg); ok {
			return m, tea.Quit
		}
		if m.viewer != nil {
			return m, m.viewer.Update(msg) // graphics cleanup still flows
		}
		return m, nil
	}
	if handled, cmd := m.debugUpdate(msg); handled {
		return m, cmd
	}
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "ctrl+l" {
		return m, m.openAddress()
	}
	if m.addressOpen {
		if handled, cmd := m.addressUpdate(msg); handled {
			return m, cmd
		}
	}
	switch v := msg.(type) {
	case loop.ScriptPermissionMsg:
		m.scriptPermission(v)
		return m, nil
	case pageLoadedMsg:
		if v.seq != m.navSeq {
			return m, nil // superseded by a later navigation
		}
		if errors.Is(v.err, context.Canceled) {
			return m, nil // cancelled by the reader
		}
		if v.err != nil {
			m.policy.Trace.Record("navigation_error", map[string]any{"url": v.finalURL, "error": v.err.Error()})
			m.documentSeq = m.navSeq
			m.status = clean("load failed: " + v.err.Error()) // errors echo page-controlled input
			return m, nil
		}
		if v.line != "" {
			m.status = clean(v.line) // not presented; the current page stays
			return m, nil
		}
		if v.video != "" {
			return m, m.startPlayer(v.video) // an overlay: history and the page stay
		}
		m.current = v.base
		m.resetView = true
		// History moves only now, on success, so Back/Forward always name
		// the page on screen.
		if v.hist == pushNew {
			m.history = append(m.history[:m.hIndex+1], v.base.String())
			m.hIndex = len(m.history) - 1
		} else if v.hist >= 0 && v.hist < len(m.history) {
			m.hIndex = v.hist
			m.history[v.hist] = v.base.String() // a redirect may have moved it
		}
		cmds := []tea.Cmd{m.closeMedia(), m.leaveForms()}
		if v.img != nil {
			view, cmd := newMediaView(v.img, v.player, m.pictureOptions(), m.cols, m.viewRows())
			m.media = view
			m.status = ""
			return m, tea.Batch(append(cmds, cmd)...)
		}
		p := m.policy
		p.Origin = v.base
		if m.profile.Fetch != profile.FetchAllowlist {
			p.AllowHosts = nil // the page's own fetch(): the hosts are for allowlist only
		}
		m.documentNav = m.loop.LoadPage(v.html, v.base.String(), netpolicy.New(p))
		m.documentSeq = m.navSeq
		m.status = clean("loading " + v.base.String())
		return m, tea.Batch(cmds...)

	case nthtml.PaintRequest:
		if v.Owner == m.viewer && m.cssPainted && v.Revision == m.gen {
			m.loop.Paint(v)
		}
		return m, nil
	case nthtml.PaintReady:
		if v.Request.Owner != m.viewer || !m.cssPainted {
			return m, nil
		}
		cmd := m.viewer.Update(v)
		if err := m.viewer.Err(); err != nil {
			m.status = clean("paint: " + err.Error())
		} else if strings.HasPrefix(m.status, "paint:") {
			m.status = ""
		}
		return m, cmd
	case loop.FrameFocusMsg:
		if v.Gen == m.gen {
			m.embeddedFocus = v.Active
		}
		return m, nil
	case loop.FrameMsg:
		if v.Navigation != 0 && !m.loop.NavigationCurrent(v.Navigation) {
			return m, nil
		}
		// Frames arrive from the render worker; drop anything older than
		// what is on screen so a slow render cannot regress the view.
		if v.Gen <= m.lastFrame {
			return m, nil
		}
		if m.resetView {
			m.embeddedFocus = false
		}
		m.lastFrame = v.Gen
		if v.Err == nil || v.Chunk.Count > 0 {
			m.gen = v.Gen
		}
		if v.Err == nil {
			m.frameRegions = v.FrameRegions
		}
		oldChunk := m.chunk
		m.chunk, m.chunkFailed = v.Chunk, v.Err != nil && v.Chunk.Count > 0
		if v.Err != nil {
			m.status = clean("render: " + v.Err.Error())
			if m.chunkFailed {
				return m, m.leaveForms()
			}
			return m, nil
		}
		warnings := append(statusWarnings(v.Result.Warnings), v.Warnings...) // nthtml's, then the loader's
		switch {
		case strings.HasPrefix(m.status, "[error] ") || strings.HasPrefix(m.status, "load failed:") || strings.HasPrefix(m.status, "blocked:") || m.pendingNav != "":
			// Reflow and resource warnings do not resolve a page/script failure
			// or answer a pending navigation confirmation.
		case len(warnings) > 0:
			m.status = clean("[page warning] " + strings.Join(warnings, "; "))
			if len(warnings) == 1 && strings.HasPrefix(warnings[0], "image ") {
				m.status = clean("[image warning] " + warnings[0])
			}
		case strings.HasPrefix(m.status, "render:") || strings.HasPrefix(m.status, "paint:"):
			m.status = ""
		case m.resetView && !strings.HasPrefix(m.status, "focus: "):
			m.status = "" // the page's first frame: "loading …" is over (a focus line set by the page stays)
		}
		if v.Painted != nil {
			return m, m.installCSSFrame(v)
		}
		if v.Chunk.Count == 0 {
			m.status = "render: missing initial CSS band"
			return m, nil
		}
		if m.viewer == nil {
			// viewerOptions leaves ReflowTypeset off, which is what the host
			// wants: the loop re-flows on Resize, at its own width, off the
			// UI goroutine; the viewer must never re-render on its own.
			opts := viewerOptions
			opts.HostPainted = false
			m.viewer = nthtml.NewWithOptions(v.Result, opts)
			m.resetView = false
			cmd := tea.Batch(m.viewer.Init(), m.viewer.SetBounds(0, 0, m.cols, m.viewRows()))
			m.loop.Resize(documentWidth(m.viewer))
			return m, tea.Batch(cmd, m.finishChunk(), m.applyRing(false))
		}
		// Re-flow in place (nthtml v0.1.0 Model.SetResult). SetResult resets
		// scroll, zoom, search and the focused link; the host puts the scroll
		// offset back from the previous crop so a page that re-flows
		// continuously stays where the reader left it. Pan clamps to the new
		// page, so a shorter page simply ends at its bottom. Zoom is not
		// recoverable through the public API (design §11).
		crop := m.viewer.Picture().Crop()
		if oldChunk.Count > 0 {
			if viewport, ok := m.viewer.DocumentViewport(); ok {
				crop = viewport
			}
		}
		cmd := m.viewer.SetResult(v.Result)
		m.loop.Resize(documentWidth(m.viewer)) // no-op unless the terminal's pixel geometry changed
		if m.resetView || crop.Empty() || v.Chunk.Count > 0 && (oldChunk.Index != v.Chunk.Index || v.Chunk.Anchor != "" || v.Chunk.Bottom) {
			scroll := m.resetView // a new page's script focus scrolls into view, as in browsers
			m.resetView = false
			return m, tea.Batch(cmd, m.finishChunk(), m.applyRing(scroll))
		}
		// Pan first: it is relative to the origin SetResult just reset to.
		pan := m.viewer.Pan(crop.Min.X, crop.Min.Y)
		return m, tea.Batch(cmd, pan, m.applyRing(false))

	case loop.ScriptNavigateMsg:
		return m, m.scriptNavigate(v)
	case loop.NavigateMsg:
		if !v.Form && m.chunkFragment(v.Href) {
			return m, nil
		}
		if !v.Form {
			if cmd, ok := m.cssFragment(v.Href); ok {
				return m, cmd
			}
		}
		return m, m.admit(v.Href, v.Form, v.Script)
	case videoFrameMsg, videoStatusMsg, videoDeadMsg, videoProgressMsg:
		return m, m.onVideo(msg)
	case loop.FocusMsg:
		return m, m.onFocus(v)
	case tea.PasteMsg:
		if handled, cmd := m.onPaste(v); handled {
			return m, cmd
		}
	case loop.ValueMsg:
		m.onValue(v)
		return m, nil
	case loop.TitleMsg:
		m.title = clean(v.Title) // page text: never trusted at the terminal boundary
	case loop.LogMsg:
		m.status = clean(fmt.Sprintf("[%s] %s", v.Level, v.Line)) // jsvm strips console text; error strings may still echo page input

	case nthtml.LinkMsg:
		if v.Owner != m.viewer || !m.frameInputReady() {
			return m, nil
		}
		// Keyboard (Tab/Enter) and mouse activation of links both arrive
		// here; the page's click listeners decide the default action first.
		m.loop.LinkActivatedAt(m.gen, v.Link)
		return m, nil
	case nthtml.CopyMsg:
		if m.profile.Clipboard != profile.ClipboardCopy {
			return m, nil
		}
		return m, tea.SetClipboard(v.Text)

	case tea.MouseClickMsg:
		if m.overlay() {
			return m, nil // nothing reaches the page beneath a picture or video
		}
		m.pressed = v.Button == tea.MouseLeft && m.frameInputReady()
		if m.chunk.Count > 0 {
			m.pressed = m.pressed && m.chunkMouseReady()
		}
		m.pressGen = m.gen
		m.pressCell = image.Pt(v.X, v.Y)
	case tea.MouseReleaseMsg:
		if m.overlay() {
			return m, nil
		}
		p := image.Pt(v.X, v.Y)
		click := m.pressed && p == m.pressCell
		if m.cssPainted && (!m.frameInputReady() || m.pressGen != m.gen) {
			m.pressed = false
			v.Button = tea.MouseRight
			return m, m.viewer.Update(v)
		}
		m.pressed = false
		if click && m.viewer != nil {
			if m.pressGen == m.gen && m.frameInputReady() {
				if link, ok := m.viewer.LinkAtCell(p); ok {
					m.loop.LinkActivatedAt(m.gen, link)
				} else if area, ok := m.viewer.CellToDocument(p); ok {
					m.loop.ClickAtFrame(m.gen, area)
				}
			}
			// Finish selection without a second activation. The host owns link
			// dispatch so scripts can preventDefault before fragment navigation.
			release := v
			release.Button = tea.MouseRight
			return m, m.viewer.Update(release)
		}

	case tea.MouseWheelMsg:
		if !m.overlay() && m.viewer != nil {
			if area, ok := m.viewer.CellToDocument(image.Pt(v.X, v.Y)); ok {
				delta := image.Point{}
				switch v.Button {
				case tea.MouseWheelDown:
					delta.Y = 40
				case tea.MouseWheelUp:
					delta.Y = -40
				case tea.MouseWheelRight:
					delta.X = 40
				case tea.MouseWheelLeft:
					delta.X = -40
				}
				for _, region := range m.frameRegions {
					if region.Overlaps(area) && delta != (image.Point{}) {
						if m.frameInputReady() {
							m.loop.ScrollFrameAt(m.gen, area, delta)
						}
						return m, nil
					}
				}
			}
		}
		if !m.overlay() && m.chunk.Count > 0 {
			key := ""
			if v.Button == tea.MouseWheelDown {
				key = "down"
			}
			if v.Button == tea.MouseWheelUp {
				key = "up"
			}
			if key != "" && m.chunkKey(key) {
				return m, nil
			}
		}
	case tea.KeyPressMsg:
		if m.downloading.Load() && v.String() == "esc" {
			m.navCancel()
			m.status = "download cancelled"
			return m, nil
		}
		if m.player != nil {
			if handled, cmd := m.playerKey(v); handled {
				return m, cmd
			}
		}
		if m.scriptPermissionKey(v.String()) {
			return m, nil
		}
		searching := m.viewer != nil && m.viewer.SearchInputActive()
		if m.pendingNav != "" && !searching {
			switch v.String() {
			case "enter":
				href := m.pendingNav
				plan := m.pendingScript
				m.pendingScript = nil
				m.pendingNav = ""
				if plan != nil {
					m.scriptNavCount = 0
					return m, m.executeScriptNavigation(*plan)
				}
				return m, m.navigate(href, pushNew, true) // the reader confirmed: any origin
			case "esc":
				m.pendingScript = nil
				m.pendingNav = ""
				m.status = clean("stayed on " + m.current.String())
				return m, nil
			}
		}
		if !searching {
			if m.glynx && m.mode == modeBrowse && !m.overlay() && !m.embeddedFocus && m.pendingNav == "" {
				switch v.String() {
				case "g":
					return m, m.openAddress()
				case "left":
					return m, m.step(-1)
				case "f":
					return m, m.step(1)
				case "right":
					v = tea.KeyPressMsg{Code: tea.KeyEnter}
				case "down":
					v = tea.KeyPressMsg{Code: tea.KeyTab}
				case "up":
					v = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
				}
				msg = v
			}
			if m.media != nil {
				switch v.String() {
				case "esc":
					return m, m.step(-1)
				case "q", "ctrl+c", "backspace", "alt+left", "alt+right":
				default:
					return m, nil
				}
			} else if handled, cmd := m.formKey(v); handled {
				return m, cmd
			}
			switch v.String() {
			case "q", "ctrl+c":
				return m, m.quit()
			case "backspace", "alt+left":
				return m, m.step(-1)
			case "alt+right":
				return m, m.step(+1)
			case "ctrl+r":
				m.scriptNavCount = 0
				if m.current != nil {
					return m, m.navigate(m.current.String(), m.hIndex, m.anyOriginFor())
				}
				return m, nil
			}
			if m.overlay() {
				return m, nil
			}
			// The terminal keeps its own navigation keys; the page gets a copy
			// as keydown and cannot swallow them. Text typed into the viewer's
			// / search box never reaches the page.
			if m.embeddedFocus {
				switch v.String() {
				case "esc":
					m.embeddedFocus = false
					m.loop.LeaveFrameFocus(m.gen)
					return m, nil
				case "up", "down", "left", "right", "pgup", "pgdown", "home", "end":
					if m.frameInputReady() {
						m.loop.ScrollFocusedFrameAt(m.gen, v.String())
					}
					return m, nil
				}
			}
			if m.frameInputReady() {
				m.loop.InputAt(m.gen, v)
			}
			if m.chunkKey(v.String()) {
				return m, nil
			}
		}

	case tea.WindowSizeMsg:
		m.cols, m.rows = v.Width, v.Height
		var media tea.Cmd
		if m.media != nil {
			media = m.media.setSize(v.Width, m.viewRows())
		}
		if m.player != nil {
			media = m.player.pic.SetSize(v.Width, m.viewRows())
			m.sendSize()
		}
		if m.viewer != nil {
			cmd := tea.Batch(media, m.viewer.SetBounds(0, 0, v.Width, m.viewRows()), m.viewer.Update(msg))
			m.loop.Resize(documentWidth(m.viewer)) // re-flow at the terminal's real pixel width
			return m, cmd
		}
		return m, media
	}
	var overlay tea.Cmd
	switch {
	case m.player != nil:
		overlay = m.player.pic.Update(msg)
	case m.media != nil:
		overlay = m.media.update(msg)
	}
	// The viewer's own picture messages must reach it even under an overlay:
	// its render job would otherwise never finish and the page stay frozen.
	// Only input stops at the overlay.
	if _, input := msg.(tea.MouseMsg); input && m.overlay() {
		return m, overlay
	}
	if m.viewer != nil {
		return m, tea.Batch(overlay, m.viewer.Update(msg))
	}
	return m, overlay
}

// overlay reports a picture or video in the page's place.
func (m *Model) overlay() bool {
	return m.player != nil || m.media != nil || len(m.scriptPermissions) > 0
}

// quit starts shutdown: graphics are cleaned up, then the program ends.
func (m *Model) quit() tea.Cmd {
	m.closing = true
	if m.navCancel != nil {
		m.navCancel()
	}
	closeCmd := tea.Batch(m.closePlayer(), m.closeMedia())
	if m.viewer != nil {
		closeCmd = tea.Batch(closeCmd, m.viewer.Close())
	}
	return tea.Sequence(closeCmd, tea.Tick(shutdownGrace, func(time.Time) tea.Msg { return shutdownReadyMsg{} }))
}

// viewRows is the viewer's height: the terminal minus the two status lines
// and, while a textarea is edited, its editor panel.
func (m *Model) viewRows() int { return max(1, m.rows-2-m.panelRows()) }

// step moves through history. Pages are always re-fetched (no cache) so a
// Back never shows state a script produced earlier.
func (m *Model) step(delta int) tea.Cmd {
	m.scriptNavCount = 0
	i := m.hIndex + delta
	if i < 0 || i >= len(m.history) {
		return nil
	}
	return m.navigate(m.history[i], i, m.anyOriginFor()) // hIndex moves when the load succeeds
}

func (m *Model) View() tea.View {
	body := ""
	if m.player != nil {
		body = m.player.pic.View()
	} else if m.media != nil {
		body = m.media.view()
	} else if m.viewer != nil {
		body = m.viewer.View()
	}
	// Everything on these two lines passed clean() when stored; truncation
	// only measures width. No page text reaches here unfiltered.
	status := statusLine(m.status, m.cols)
	if line, ok := m.formStatus(); ok && m.pendingNav == "" && len(m.scriptPermissions) == 0 {
		status = line // the editor or chooser; a confirm prompt always wins (pendingNav)
	}
	if m.player != nil {
		status = ansi.Truncate(m.playerStatus(), max(1, m.cols), "…")
	}
	if prompt := m.scriptPermissionStatus(); prompt != "" {
		status = ansi.Truncate(prompt, max(1, m.cols), "…")
	}
	if m.addressOpen {
		status = m.address.View()
	}
	pos := ""
	if m.chunk.Count > 0 {
		pos = fmt.Sprintf(" · chunk %d/%d · [ ] chunks", m.chunk.Index+1, m.chunk.Count)
	}
	if len(m.history) > 1 {
		pos += fmt.Sprintf(" · %d/%d", m.hIndex+1, len(m.history))
	}
	enforced := ""
	if m.notEnforced {
		enforced = " (not OS-enforced)"
	}
	name, keys := "ntbrowser", "Tab focus · Enter open · ⌫ back · ⌥→ fwd · ^L address"
	if m.glynx {
		name, keys = "glynx", "↑↓ focus · → open · ← back · f fwd · g address/search"
	}
	help := "\n " + name + " · " + ansi.Truncate(clean(m.title), 40, "…") + pos + " · profile " + clean(m.profile.Name) + enforced + " · " + keys + " · ^R reload · / search · q quit"
	if h := m.helpLine(); h != "" {
		help = "\n" + h
	}
	if m.player != nil {
		help = "\n Space pause · ←→ seek · Esc back · ^C quit"
	}
	if len(m.scriptPermissions) > 0 {
		help = "\n Alt+O allow once · Alt+S session · Esc deny · q quit"
		if ansi.StringWidth(m.scriptPermissionStatus()) > m.cols {
			help = "\n Widen terminal to view both origins before allowing · Esc deny"
		}
	}
	if m.addressOpen {
		help = "\n Enter open · Esc cancel · Ctrl+U clear · Ctrl+C quit"
		if m.addressError != "" {
			help = "\n" + chromeLine(m.addressError, m.cols, errorLineStyle)
		}
	}
	if m.debug.open {
		body += "\n" + m.debugView()
	}
	v := tea.NewView(body + "\n" + status + help)
	v.AltScreen = true
	if !m.closing {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// readLocal is the host's own file access, bounded like nthtml's parser.
// It lives in the app, never in jsvm.
var readLocal = func(path string) (string, error) {
	return "", fmt.Errorf("local files disabled in this build: %s", path)
}

// SetLocalReader lets cmd/ntbrowser install an os-backed reader.
func SetLocalReader(open func(path string) (io.ReadCloser, error)) {
	readLocal = func(path string) (string, error) {
		r, err := open(path)
		if err != nil {
			return "", err
		}
		defer r.Close()
		b, err := io.ReadAll(io.LimitReader(r, int64(document.DefaultLimits.Bytes)+1))
		if err != nil {
			return "", err
		}
		if len(b) > document.DefaultLimits.Bytes {
			return "", document.ErrLimit
		}
		return string(b), nil
	}
}

// statusWarnings drops nthtml's notes about SVG elements it skipped: browsers
// ignore those elements silently and the rest of the SVG still draws, so they
// would only push real warnings off the status line.
func statusWarnings(warnings []string) []string {
	var out []string
	for _, w := range warnings {
		if !strings.HasPrefix(w, "svg: skipped unsupported element ") {
			out = append(out, w)
		}
	}
	return out
}

// SetDiagnostics installs the host recorder before the program starts.
func (m *Model) SetDiagnostics(r *diagnostics.Recorder) {
	if m.profile.Diagnostics {
		m.policy.Trace = r
	}
}

func (m *Model) SetCookies(s *cookies.Store) {
	if m.profile.Cookies == profile.CookiesSession {
		m.policy.Cookies = s
	}
}

// SetGlynx selects the browsing interface independently of the security profile.
func (m *Model) SetGlynx() { m.glynx = true }
