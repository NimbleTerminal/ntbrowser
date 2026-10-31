// Package app is the Bubble Tea host. It owns the terminal, the nthtml viewer,
// navigation policy and history. It knows nothing about JavaScript beyond the
// loop's messages.
package app

import (
	"context"
	"fmt"
	"image"
	"io"
	"net/url"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"

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
}

// pushNew means "truncate forward history and append" (a link, the start
// page, a confirmed prompt); any other value is Back/Forward/Reload.
const pushNew = -1

type shutdownReadyMsg struct{}

const shutdownGrace = 150 * time.Millisecond

type Model struct {
	loop    *loop.Loop
	viewer  *nthtml.Model
	profile profile.Profile  // frozen at launch
	policy  netpolicy.Policy // template derived from it; Origin is set per navigation
	nav     NavMode
	cols    int
	rows    int
	gen     uint64
	title   string
	status  string
	closing bool
	start   string

	current    *url.URL
	history    []string
	hIndex     int // index of current in history; -1 before the first load
	pendingNav string

	// resetView marks the next frame as the first of a new page: scroll is
	// not carried over. pressCell/pressed turn a press+release on the same
	// cell into an element click (browser semantics, and the same rule nthtml
	// uses before it emits LinkMsg).
	resetView bool
	pressCell image.Point
	pressed   bool
	// navSeq counts navigations started; a pageLoadedMsg carrying an older
	// sequence is a slower, superseded load and is dropped. History is only
	// touched when a load succeeds (decision 20).
	navSeq uint64

	// Forms (forms.go): the input mode, the last focus report, and the
	// editor and chooser state.
	mode   inputMode
	focus  loop.FocusMsg
	ring   string     // the element the viewer's focus ring is on ("" = none)
	media  *mediaView // a picture shown in the page's place (content.go)
	input  textinput.Model
	area   textarea.Model
	choice int
}

// viewerOptions tunes the nthtml viewer for a page that re-flows often and
// scrolls by keypress (perf analysis 2026-10-27, HN front page). StableID
// keeps the Kitty image id across frames while geometry is unchanged, so a
// re-flow or scroll step changes pixels but not the placeholder grid and
// Bubble Tea has nothing to repaint; without it every keypress redrew all
// cells. Zlib frames encode about three times faster than PNG and are
// cheaper for the terminal to decode. ReflowTypeset stays off (decision 19).
var viewerOptions = nthtml.ModelOptions{
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

func (m *Model) Init() tea.Cmd { return m.navigate(m.start, pushNew, true) }

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
// page cannot navigate on its own (no location.assign); every navigation is
// a user action, filtered by the NavMode in Update.
func (m *Model) navigate(href string, hist int, anyOrigin bool) tea.Cmd {
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
	policy.Origin, policy.AnyOrigin, policy.MaxBody = m.current, anyOrigin, 1<<20
	policy.Timeout = navTimeout
	gates := m.profile
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
		ctx, cancel := context.WithTimeout(context.Background(), navTimeout)
		defer cancel()
		msg := load(ctx, f, u.String(), gates, pageLoadedMsg{seq: seq, hist: hist})
		msg.base, _ = url.Parse(msg.finalURL)
		return msg
	}
}

// anyOriginFor is the navigation policy for a URL the user already admitted
// (Back, Forward, Reload): only -nav allowlist keeps redirects confined.
func (m *Model) anyOriginFor() bool { return m.nav != NavAllowlist }

// admit applies the NavMode to a link the page did not prevent, or to a form
// submission (same gate, its own confirmation wording).
func (m *Model) admit(href string, form bool) tea.Cmd {
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
	if m.closing {
		if _, ok := msg.(shutdownReadyMsg); ok {
			return m, tea.Quit
		}
		if m.viewer != nil {
			return m, m.viewer.Update(msg) // graphics cleanup still flows
		}
		return m, nil
	}
	switch v := msg.(type) {
	case pageLoadedMsg:
		if v.seq != m.navSeq {
			return m, nil // superseded by a later navigation
		}
		if v.err != nil {
			m.status = clean("load failed: " + v.err.Error()) // errors echo page-controlled input
			return m, nil
		}
		if v.line != "" {
			m.status = clean(v.line) // not presented; the current page stays
			return m, nil
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
			view, cmd := newMediaView(v.img, v.player, m.cols, m.viewRows())
			m.media = view
			m.status = ""
			return m, tea.Batch(append(cmds, cmd)...)
		}
		p := m.policy
		p.Origin = v.base
		if m.profile.Fetch != profile.FetchAllowlist {
			p.AllowHosts = nil // the page's own fetch(): the hosts are for allowlist only
		}
		m.loop.LoadPage(v.html, v.base.String(), netpolicy.New(p))
		m.status = clean("loading " + v.base.String())
		return m, tea.Batch(cmds...)

	case loop.FrameMsg:
		// Frames arrive from the render worker; drop anything older than
		// what is on screen so a slow render cannot regress the view.
		if v.Gen <= m.gen {
			return m, nil
		}
		m.gen = v.Gen
		if v.Err != nil {
			m.status = clean("render: " + v.Err.Error())
			return m, nil
		}
		warnings := append(statusWarnings(v.Result.Warnings), v.Warnings...) // nthtml's, then the loader's
		switch {
		case len(warnings) > 0:
			m.status = clean(strings.Join(warnings, "; ")) // warnings replace the status; a log line survives the next frame
		case m.resetView && !strings.HasPrefix(m.status, "focus: "):
			m.status = "" // the page's first frame: "loading …" is over (a focus line set by the page stays)
		}
		if m.viewer == nil {
			// viewerOptions leaves ReflowTypeset off, which is what the host
			// wants: the loop re-flows on Resize, at its own width, off the
			// UI goroutine; the viewer must never re-render on its own.
			m.viewer = nthtml.NewWithOptions(v.Result, viewerOptions)
			m.resetView = false
			cmd := tea.Batch(m.viewer.Init(), m.viewer.SetBounds(0, 0, m.cols, m.viewRows()))
			m.loop.Resize(documentWidth(m.viewer))
			return m, tea.Batch(cmd, m.applyRing(false))
		}
		// Re-flow in place (nthtml v0.1.0 Model.SetResult). SetResult resets
		// scroll, zoom, search and the focused link; the host puts the scroll
		// offset back from the previous crop so a page that re-flows
		// continuously stays where the reader left it. Pan clamps to the new
		// page, so a shorter page simply ends at its bottom. Zoom is not
		// recoverable through the public API (design §11).
		crop := m.viewer.Picture().Crop()
		cmd := m.viewer.SetResult(v.Result)
		m.loop.Resize(documentWidth(m.viewer)) // no-op unless the terminal's pixel geometry changed
		if m.resetView || crop.Empty() {
			scroll := m.resetView // a new page's script focus scrolls into view, as in browsers
			m.resetView = false
			return m, tea.Batch(cmd, m.applyRing(scroll))
		}
		// Pan first: it is relative to the origin SetResult just reset to.
		pan := m.viewer.Pan(crop.Min.X, crop.Min.Y)
		return m, tea.Batch(cmd, pan, m.applyRing(false))

	case loop.NavigateMsg:
		return m, m.admit(v.Href, v.Form)
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
		if v.Owner != m.viewer {
			return m, nil
		}
		// Keyboard (Tab/Enter) and mouse activation of links both arrive
		// here; the page's click listeners decide the default action first.
		m.loop.LinkActivated(v.Link)
		return m, nil
	case nthtml.CopyMsg:
		if m.profile.Clipboard != profile.ClipboardCopy {
			return m, nil
		}
		return m, tea.SetClipboard(v.Text)

	case tea.MouseClickMsg:
		m.pressed = v.Button == tea.MouseLeft
		m.pressCell = image.Pt(v.X, v.Y)
	case tea.MouseReleaseMsg:
		p := image.Pt(v.X, v.Y)
		click := m.pressed && p == m.pressCell
		m.pressed = false
		if click && m.viewer != nil {
			// A link under the cell is nthtml's: it emits LinkMsg for this
			// same release and the loop handles it once. Anything else is an
			// element click the viewer knows nothing about.
			if _, isLink := m.viewer.LinkAtCell(p); !isLink {
				if area, ok := cellToDocument(m.viewer, p); ok {
					m.loop.ClickAt(area)
				}
			}
		}

	case tea.KeyPressMsg:
		searching := m.viewer != nil && m.viewer.SearchInputActive()
		if m.pendingNav != "" && !searching {
			switch v.String() {
			case "enter":
				href := m.pendingNav
				m.pendingNav = ""
				return m, m.navigate(href, pushNew, true) // the reader confirmed: any origin
			case "esc":
				m.pendingNav = ""
				m.status = clean("stayed on " + m.current.String())
				return m, nil
			}
		}
		if !searching {
			if m.media != nil {
				if v.String() == "esc" {
					return m, m.step(-1)
				}
			} else if handled, cmd := m.formKey(v); handled {
				return m, cmd
			}
			switch v.String() {
			case "q", "ctrl+c":
				m.closing = true
				var closeCmd tea.Cmd
				if m.viewer != nil {
					closeCmd = m.viewer.Close()
				}
				return m, tea.Sequence(closeCmd, tea.Tick(shutdownGrace, func(time.Time) tea.Msg { return shutdownReadyMsg{} }))
			case "backspace", "alt+left":
				return m, m.step(-1)
			case "alt+right":
				return m, m.step(+1)
			case "ctrl+r":
				if m.current != nil {
					return m, m.navigate(m.current.String(), m.hIndex, m.anyOriginFor())
				}
				return m, nil
			}
			// The terminal keeps its own navigation keys; the page gets a copy
			// as keydown and cannot swallow them. Text typed into the viewer's
			// / search box never reaches the page.
			m.loop.Input(v)
		}

	case tea.WindowSizeMsg:
		m.cols, m.rows = v.Width, v.Height
		var media tea.Cmd
		if m.media != nil {
			media = m.media.setSize(v.Width, m.viewRows())
		}
		if m.viewer != nil {
			cmd := tea.Batch(media, m.viewer.SetBounds(0, 0, v.Width, m.viewRows()), m.viewer.Update(msg))
			m.loop.Resize(documentWidth(m.viewer)) // re-flow at the terminal's real pixel width
			return m, cmd
		}
		return m, media
	}
	if m.media != nil {
		return m, m.media.update(msg) // the picture shown in the page's place
	}
	if m.viewer != nil {
		return m, m.viewer.Update(msg)
	}
	return m, nil
}

// viewRows is the viewer's height: the terminal minus the two status lines
// and, while a textarea is edited, its editor panel.
func (m *Model) viewRows() int { return max(1, m.rows-2-m.panelRows()) }

// step moves through history. Pages are always re-fetched (no cache) so a
// Back never shows state a script produced earlier.
func (m *Model) step(delta int) tea.Cmd {
	i := m.hIndex + delta
	if i < 0 || i >= len(m.history) {
		return nil
	}
	return m.navigate(m.history[i], i, m.anyOriginFor()) // hIndex moves when the load succeeds
}

func (m *Model) View() tea.View {
	body := ""
	if m.media != nil {
		body = m.media.view()
	} else if m.viewer != nil {
		body = m.viewer.View()
	}
	// Everything on these two lines passed clean() when stored; truncation
	// only measures width. No page text reaches here unfiltered.
	status := ansi.Truncate(clean(m.status), max(1, m.cols), "…")
	if line, ok := m.formStatus(); ok && m.pendingNav == "" {
		status = line // the editor or chooser; a confirm prompt always wins (pendingNav)
	}
	pos := ""
	if len(m.history) > 1 {
		pos = fmt.Sprintf(" · %d/%d", m.hIndex+1, len(m.history))
	}
	help := "\n ntbrowser · " + ansi.Truncate(clean(m.title), 40, "…") + pos + " · profile " + clean(m.profile.Name) + " · Tab focus · Enter open · ⌫ back · ⌥→ fwd · ^R reload · / search · q quit"
	if h := m.helpLine(); h != "" {
		help = "\n" + h
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
