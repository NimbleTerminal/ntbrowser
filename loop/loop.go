// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package loop is the orchestrator. One goroutine (the VM goroutine) owns the
// Goja runtime, the dom.Tree, the timer heap and the frame scheduler. Bubble
// Tea's Update goroutine never touches the VM: it posts input through Input
// and LinkActivated and receives FrameMsg / NavigateMsg / LogMsg through the
// Sink. nthtml renders on a worker goroutine from an immutable HTML snapshot.
//
// Neither side ever blocks on the other: the UI posts with tryPost (drop on
// full), and the VM sends through a buffered outbox drained by a forwarder
// goroutine, so a busy Bubble Tea program cannot stall a watchdog slice and a
// slow page cannot stall the terminal.
//
//	[Terminal Key/Mouse] -> tea.Update -> loop.Input ----(inbox)----> VM goroutine
//	                                                                   | dispatch to JS
//	                                                                   | tree dirty?
//	                                                                   v
//	tea.Update <- (outbox) <- FrameMsg(*document.Result) <- render worker <- tree.Serialize()
package loop

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/dop251/goja"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/devtools"
	"nimbleterminal.dev/ntbrowser/diagnostics"
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sessionstorage"
	"nimbleterminal.dev/ntbrowser/subres"
)

// Sink is satisfied by *tea.Program. It may block; the loop never calls it
// from the VM goroutine directly.
type Sink interface{ Send(tea.Msg) }

// Messages the loop sends to the Bubble Tea model.
type (
	FrameMsg struct {
		FrameImage   image.Image
		FrameOrigin  image.Point
		FrameRegions []image.Rectangle
		Painted      *nthtml.PaintedResult // CSS layout and initial band, installed atomically
		Navigation   uint64
		Gen          uint64 // monotonically increasing; stale frames are dropped by the host
		Result       *document.Result
		Err          error
		// Warnings are the loader's (a refused stylesheet, an oversize
		// image); Result.Warnings stay nthtml's. Page-controlled text:
		// the host passes them through app.clean.
		Warnings []string
		Chunk    ChunkInfo
	}
	// NavigateMsg carries an absolute URL already resolved against the page
	// base. Admission (scheme, host) is the host's decision.
	// Form marks a form submission (the host words its confirmation
	// differently; the admission gate is the same as for links).
	NavigateMsg struct {
		Href   string
		Form   bool
		Script bool // script-origin form submissions are not reader activation
	}
	LogMsg   struct{ Level, Line string }
	TitleMsg struct{ Title string }
)

// Page is one navigation: tree + sandbox + policy. Replacing it discards the
// whole JS heap, which is how cross-page isolation is achieved.
type Page struct {
	diagramsDisabled               bool
	debugNavigation, debugRevision uint64
	domPins                        map[jsvm.DOMRef]bool
	frameScroll                    image.Point
	scrollEvent                    bool
	frameID                        uint64
	frames                         map[dom.NodeID]*childFrame
	displayedFrames                map[dom.NodeID]framePresentation
	frameFocus                     dom.NodeID

	scriptPermissions map[uint64]*scriptPermission

	dynamicOrdered []*dynamicScript

	pendingLocation  *ScriptNavigateMsg
	locationRequests int
	Tree             *dom.Tree
	Sandbox          *jsvm.Sandbox
	Fetcher          *netpolicy.Fetcher
	Base             string
	loaded           bool
	loadComplete     bool
	dynamicPending   int
	// ctx bounds every request this page starts; cancel fires when the page
	// is replaced or killed, so no completion outlives its runtime.
	ctx         context.Context
	cancel      context.CancelFunc
	paintCtx    context.Context
	paintCancel context.CancelFunc
	killed      bool // runtime dropped (heap or strike budget); the last frame stays
	// boxes are the painted boxes of the last frame shown for this page,
	// used to map a document-pixel hit back to an element (Box.Anchor = id).
	boxes []document.Box
	// result is the last frame's Result (SourceAt refines clicks, forms.go).
	hoverSource string
	result      *document.Result
	frameGen    uint64
	chunkState  *chunkRender // transferred between serial workers; never inspected by VM
	chunkReq    chunkRequest
	chunkInfo   ChunkInfo
	chunkIndex  chunkLookup
	// lastFocus is the focus the host was last told about; focusValue the
	// focused control's value at focus or last commit (change detection).
	lastFocus     dom.NodeID
	focusValue    string
	autofocusDone bool
	// formTasks are submit/reset requests made by script (form hooks); they
	// run after the task that made them, never inside the VM.
	formTasks []func()
	// firing and resetting mark forms whose submit or reset event is being
	// dispatched, so a listener cannot re-request it (forms review #1).
	firing, resetting map[dom.NodeID]bool
	// subres loads the page's stylesheets and images (nil when off).
	subres *subres.Session
	// firstFrame is true until the first frame of this page has rendered;
	// stylesheets discovered while it is true block that frame (decision 3)
	// until styleDeadline (StyleTimeout elapsed) releases it.
	firstFrame    bool
	styleDeadline bool
	// backgrounds are the CSS background sources the last frame reported.
	backgrounds []string
	// budgetWarned remembers which hrefs were already reported over the
	// rule budget, so the status line is not flooded every frame.
	budgetWarned map[string]bool
	// refused holds hrefs nthtml would not render with: dropped for the
	// rest of the page's life (decision 6's belt, independent of CountRules).
	refused map[string]bool
}

type Config struct {
	DOMLimits                      dom.Limits
	FrameHeight                    int // nonzero only for embedded child raster viewports
	frameHub                       *frameHub
	frameOwner                     *sync.Mutex
	frameID, frameParent, frameTop uint64
	frameDepth                     int
	frameOpaque                    bool

	Trace       *diagnostics.Recorder
	Chunked     bool          // opt-in semantic chunk renderer; CSS renderer remains the default
	Width       int           // document pixel width handed to nthtml (default 800)
	MinFrame    time.Duration // minimum spacing between re-flows (default 33ms)
	RenderLimit document.Limits
	Fonts       document.FaceSource
	Inbox       int // buffered tasks (default 256)
	Outbox      int // buffered messages to the UI (default 1024)
	Script      jsvm.Config
	// Profile is the frozen security profile. Its zero value has every gate
	// closed: no script runs and fetch() is refused.
	Profile profile.Profile
	// Subresources bounds stylesheet and image loading; zero fields take
	// subres defaults. SubresourceTransport builds the transport for a page
	// base (nil, or Subresources.Mode == subres.ModeOff, loads nothing).
	Subresources         subres.Config
	SubresourceTransport func(base string) subres.Transport
}

type Loop struct {
	scriptSessions     map[profile.ScriptGrant]bool
	scriptPermissionID uint64

	storage         *sessionstorage.Session
	frameNavigation atomic.Uint64
	locationCommit  atomic.Uint64
	resizeWidth     atomic.Int64
	resizeWake      chan struct{}
	cfg             Config
	sink            Sink
	inbox           chan func()
	outbox          chan tea.Msg
	ctx             context.Context

	page         *Page
	timers       timerHeap
	nextID       int
	frames       []frameCB
	activeFrames []frameCB

	currentFrame   atomic.Uint64
	renderJobs     chan func(*paintWorker)
	paintWake      chan struct{}
	paintQueue     paintQueue
	gen            atomic.Uint64
	navGen         atomic.Uint64 // LoadPage calls, so a stale load can never win
	renderInFlight bool
	pendingFrame   bool
	lastFrame      time.Time
	frameTimer     *time.Timer
	timerT         *time.Timer  // armed to the earliest JS timer deadline
	dropped        atomic.Int64 // input events / log lines dropped under back-pressure
}

func New(ctx context.Context, cfg Config, sink Sink) *Loop {
	if cfg.frameOwner == nil {
		cfg.frameOwner = &sync.Mutex{}
	}
	if !cfg.Profile.Diagnostics {
		cfg.Trace = nil
	}
	if cfg.Width <= 0 {
		cfg.Width = 800
	}
	if cfg.Profile.FrameMillis > 0 {
		cfg.MinFrame = time.Duration(cfg.Profile.FrameInterval()) * time.Millisecond
	}
	if cfg.MinFrame <= 0 {
		cfg.MinFrame = 33 * time.Millisecond
	}
	if cfg.Inbox <= 0 {
		cfg.Inbox = 256
	}
	if cfg.Outbox <= 0 {
		cfg.Outbox = 1024
	}
	if cfg.RenderLimit.Rules <= 0 || cfg.RenderLimit.Rules > document.MaxLimits.Rules {
		cfg.RenderLimit.Rules = 4096 // conservative host default; explicit higher values remain bounded by nthtml
	}
	// Host-only styles have their own allowance; script-mutable DOM ceilings
	// stay unchanged. The serialized CSS twin must also fit nthtml's input cap.
	cfg.DOMLimits.ExternalStyleBytes = max(0, min(cfg.DOMLimits.ExternalStyleBytes, dom.MaxExternalStyleBytes))
	if cfg.DOMLimits.ExternalStyleBytes > 0 {
		content := dom.DefaultLimits.TotalBytes
		if cfg.DOMLimits.TotalBytes > 0 {
			content = min(content, cfg.DOMLimits.TotalBytes)
		}
		if cfg.RenderLimit.Bytes == 0 {
			cfg.RenderLimit.Bytes = content + cfg.DOMLimits.ExternalStyleBytes
		}
		cfg.DOMLimits.ExternalStyleBytes = min(cfg.DOMLimits.ExternalStyleBytes, max(0, min(cfg.RenderLimit.Bytes, document.MaxLimits.Bytes)-content))
	}
	cfg.Subresources = cfg.Subresources.WithDefaults()
	l := &Loop{ctx: ctx, cfg: cfg, sink: sink, inbox: make(chan func(), cfg.Inbox), outbox: make(chan tea.Msg, cfg.Outbox), timerT: stoppedTimer(), frameTimer: stoppedTimer()}
	if cfg.Profile.SessionStorage == profile.SessionStorageOn {
		l.storage = sessionstorage.New(cfg.Profile.StorageBytes(), cfg.Profile.StorageTotalBytes())
	}
	l.resizeWake = make(chan struct{}, 1)
	l.renderJobs = make(chan func(*paintWorker), 1)
	l.paintWake = make(chan struct{}, 1)
	l.paintQueue.viewport = nthtml.PaintViewport{Cols: 80, Rows: 24}
	go l.runRenderer()
	go l.forward()
	return l
}

// Run is the VM goroutine. It is the only goroutine that may touch l.page.
// All realms in a frame tree serialize their turns. Each VM is still entered
// only by its own loop; synchronous foreign DOM operations touch plain DOM
// data under this same lock, never another runtime's JS objects.
func (l *Loop) Run() {
	defer func() {
		l.cfg.frameOwner.Lock()
		defer l.cfg.frameOwner.Unlock()
		l.scriptSessions = nil
		if l.page != nil {
			l.dropPage(l.page)
		}
		l.storage.Clear()
	}()
	for {
		var task func()
		select {
		case <-l.ctx.Done():
			return
		case task = <-l.inbox:
		case <-l.resizeWake:
			task = func() {
				width := int(l.resizeWidth.Load())
				if width > 0 && width != l.cfg.Width {
					l.cfg.Width = width
					l.pendingFrame = true
					l.scheduleFrame()
				}
			}
		case <-l.timerT.C:
			task = l.fireTimers
		case <-l.frameTimer.C:
			task = l.startRender
		}
		func() {
			l.cfg.frameOwner.Lock()
			defer l.cfg.frameOwner.Unlock()
			if l.ctx.Err() != nil {
				return
			}
			task()
			l.afterTask()
		}()
	}
}

// forward drains the outbox into the (possibly blocking) sink.
func (l *Loop) forward() {
	for {
		select {
		case <-l.ctx.Done():
			return
		case m := <-l.outbox:
			l.sink.Send(m)
		}
	}
}

// send never blocks the VM goroutine. Frames, navigation and titles are
// never dropped (the outbox is sized for them; if it is ever full they
// block, which only happens when the UI goroutine is dead). Log lines are
// droppable.
func (l *Loop) send(m tea.Msg) {
	switch v := m.(type) {
	case LogMsg:
		var frame, revision uint64
		if l.page != nil {
			revision = l.page.debugRevision
			if l.cfg.frameParent != 0 {
				frame = l.page.frameID
			}
		}
		l.cfg.Trace.Record("console", map[string]any{"Level": v.Level, "Line": v.Line, "frame": frame, "revision": revision})
	case FrameMsg:
		if v.Err != nil {
			l.cfg.Trace.Record("render_error", v.Err.Error())
		}
		if len(v.Warnings) > 0 {
			l.cfg.Trace.Record("warnings", v.Warnings)
		}
	}
	if _, isLog := m.(LogMsg); isLog {
		select {
		case l.outbox <- m:
		default:
			l.dropped.Add(1)
		}
		return
	}
	select {
	case l.outbox <- m:
	case <-l.ctx.Done():
	}
}

func stoppedTimer() *time.Timer {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return t
}

// armTimer points the single OS timer at the earliest JS deadline.
func (l *Loop) armTimer() {
	l.timerT.Stop()
	if len(l.timers) == 0 {
		return
	}
	l.timerT.Reset(max(0, time.Until(l.timers[0].when)))
}

// post queues work for the VM goroutine and blocks if the inbox is full. Use
// it only from worker goroutines (fetch results, render completions), never
// from the Bubble Tea goroutine.
func (l *Loop) post(fn func()) {
	select {
	case l.inbox <- fn:
	case <-l.ctx.Done():
	}
}

// tryPost drops work when the page cannot keep up. Terminal input uses it so
// a slow script degrades the page, never the terminal.
func (l *Loop) tryPost(fn func()) bool {
	select {
	case l.inbox <- fn:
		return true
	default:
		l.dropped.Add(1)
		return false
	}
}

// ---- page lifecycle ----

// LoadPage parses HTML on the VM goroutine, builds a fresh sandbox, runs the
// page's scripts in document order, fires DOMContentLoaded once every script
// has run, and schedules the first frame. Safe to call from the UI goroutine:
// it hands off without blocking.
func (l *Loop) LoadPage(html string, base string, fetcher *netpolicy.Fetcher) uint64 {
	l.currentFrame.Store(0)
	gen := l.navGen.Add(1) // taken synchronously on the caller: call order == generation order
	if fetcher == nil {
		fetcher = netpolicy.New(netpolicy.Policy{}) // the zero policy denies everything; never a nil dereference on a worker
	}
	task := func() {
		if gen != l.navGen.Load() {
			return // a newer LoadPage exists; whichever order the handoffs ran in, it wins
		}
		if l.page != nil {
			l.page.paintCancel()
			l.dropPage(l.page)
		}
		l.timers = nil
		l.frames = nil
		l.activeFrames = nil
		l.cfg.Trace.Record("document", map[string]any{"url": base, "navigation": gen, "html": html})
		tree, err := dom.Parse(strings.NewReader(html), l.cfg.DOMLimits)
		if err != nil {
			l.page = nil
			l.send(LogMsg{"error", "parse: " + err.Error()})
			return
		}
		cfg := l.cfg.Script
		cfg.Location = base
		cfg.SessionStorage = l.storage.Open(base)
		cfg.CookieRead, cfg.CookieWrite = nil, nil
		if l.cfg.Profile.Cookies == profile.CookiesSession {
			cfg.CookieRead = func() string { return fetcher.DocumentCookie(base) }
			cfg.CookieWrite = func(v string) { fetcher.SetDocumentCookie(base, v) }
		}
		cfg.AllowDynamicCode = l.cfg.Profile.AllowsDynamicCode()
		if ms := l.cfg.Profile.ClockMillis; ms > 0 {
			cfg.ClockResolution = time.Duration(ms) * time.Millisecond
		}
		if l.cfg.Profile.Media == profile.MediaVideo {
			tree.MarkVideos()
		}
		page := &Page{debugNavigation: gen, debugRevision: devtools.NextRevision(), Tree: tree, Fetcher: fetcher, Base: base, firstFrame: true, budgetWarned: map[string]bool{}, refused: map[string]bool{}, firing: map[dom.NodeID]bool{}, resetting: map[dom.NodeID]bool{}}
		page.ctx, page.cancel = context.WithCancel(l.ctx)
		page.paintCtx, page.paintCancel = context.WithCancel(l.ctx)
		cfg.DynamicScripts = l.cfg.Profile.ScriptsOn()
		cfg.LoadScript = func(req jsvm.ScriptRequest) { l.loadDynamicScript(page, gen, req) }
		cfg.Navigate = nil
		if l.cfg.Profile.ScriptsOn() && l.cfg.Profile.NavigationOpen() && l.cfg.Profile.ScriptNavigation != "" && l.cfg.Profile.ScriptNavigation != profile.ScriptNavOff {
			cfg.Navigate = func(href, mode string) error {
				if page.locationRequests >= l.cfg.Profile.ScriptNavBudget() {
					return errors.New("script navigation request budget exceeded")
				}
				page.pendingLocation = &ScriptNavigateMsg{Href: href, Mode: mode, Source: page.Base, Navigation: gen}
				return nil
			}
		}
		l.setupFrameRealm(page, &cfg)
		l.installFrameScroll(page, &cfg)
		page.Sandbox = jsvm.New(cfg, tree, (*host)(l))
		page.Sandbox.SetFormHooks(l.formHooks(page))
		l.page = page
		l.startSubresources(page)
		l.reconcileFrames()
		l.send(TitleMsg{tree.Title()})
		l.runScripts(page, 0)
		l.scheduleFrame()
	}
	// Never block the UI goroutine: the inbox normally has room; if it is
	// full, hand off in a goroutine. Ordering is irrelevant because of gen.
	if !l.tryPost(task) {
		go l.post(task)
	}
	return gen
}

// dropPage discards a page's runtime: scripting is closed so the Goja heap
// is unreachable, in-flight requests are cancelled, timers and frames are
// gone. For a page that tripped the heap or strike budget the tree and the
// last frame stay (links keep working, nothing else does); a replaced page
// is simply forgotten. The GC runs once so the budget is actually reclaimed
// before the next page starts, which is the mitigation §9 promises.
func (l *Loop) dropPage(page *Page) {
	if page.killed {
		return
	}
	page.killed = true
	l.closeFrames(page)
	for _, request := range page.scriptPermissions {
		request.timer.Stop()
	}
	page.scriptPermissions = nil
	page.dynamicOrdered = nil
	if page.subres != nil {
		page.subres.Close()
	}
	page.Sandbox.Close()
	if page.cancel != nil {
		page.cancel()
	}
	l.timers = nil
	l.frames = nil
	l.activeFrames = nil
	runtime.GC()
}

// runScripts executes tree.Scripts[i:] in order. An external script suspends
// the sequence until its body arrives, preserving document order; the
// sequence resumes on the VM goroutine.
func (l *Loop) runScripts(page *Page, i int) {
	if !l.cfg.Profile.ScriptsOn() {
		l.cfg.Trace.Record("scripts_skipped", "scripts gate off")
		page.loaded = true
		return // the scripts gate: not even DOMContentLoaded
	}
	for ; i < len(page.Tree.Scripts); i++ {
		sc := page.Tree.Scripts[i]
		l.cfg.Trace.Record("script", map[string]any{"page": page.Base, "index": i, "src": sc.Src, "inline_bytes": len(sc.Inline), "external_allowed": l.cfg.Profile.Scripts == profile.ScriptsExternal})
		switch {
		case sc.Inline != "":
			l.evalScript(page, fmt.Sprintf("%s#inline-%d", page.Base, i), sc.Inline)
		case sc.Src != "" && l.cfg.Profile.Scripts == profile.ScriptsExternal:
			next := i + 1
			generation := l.navGen.Load()
			l.fetchScript(page, generation, sc.Src, sc.Integrity, sc.CrossOrigin, func(resp netpolicy.Response, err error) {
				if err != nil {
					l.report(err)
				} else {
					l.evalScript(page, sc.Src, resp.Body)
				}
				l.runScripts(page, next)
			})
			return
		}
	}
	if !page.loaded {
		page.loaded = true
		l.report(page.Sandbox.SetDocumentReady("interactive"))
		_, err := page.Sandbox.Dispatch(jsvm.Event{Type: "DOMContentLoaded", NonCancelable: true})
		l.report(err)
	}
}

func (l *Loop) report(err error) {
	if err != nil {
		l.send(LogMsg{"error", err.Error()})
	}
}

// ---- input routing from the Bubble Tea goroutine ----

// Input forwards a key press as a keydown to the focused element (falling
// back to document) and lets it bubble. It is dropped before reaching the VM
// when the page registered no keydown listener, so an idle page costs
// nothing.
func (l *Loop) Input(key tea.KeyPressMsg) { l.InputAt(0, key) }

// InputAt rejects keys queued against a superseded DOM/frame.
func (l *Loop) InputAt(gen uint64, key tea.KeyPressMsg) {
	l.tryPost(func() {
		if l.page == nil || gen != 0 && (!l.FrameCurrent(gen) || l.page.Tree.Dirty()) {
			return
		}
		if l.page.frameFocus != 0 {
			if shown, ok := l.page.displayedFrames[l.page.frameFocus]; ok && shown.frame.child != nil && shown.frame.revision == shown.revision {
				shown.frame.child.InputAt(shown.generation, key)
			}
			return
		}
		if !l.page.Sandbox.HasListeners("keydown") {
			return
		}
		_, err := l.page.Sandbox.Dispatch(jsvm.Event{Type: "keydown", Key: domKeyName(key)})
		l.report(err)
	})
}

// domKeyName maps Bubble Tea keystroke names to DOM KeyboardEvent.key values
// for the common keys; printable text passes through unchanged.
func domKeyName(k tea.KeyPressMsg) string {
	if k.Text != "" && k.Mod == 0 {
		return k.Text
	}
	name := k.Keystroke()
	if dom, ok := domKeys[name]; ok {
		return dom
	}
	return name // e.g. "ctrl+s": not a DOM name, but stable and documented
}

var domKeys = map[string]string{
	"enter": "Enter", "esc": "Escape", "escape": "Escape", "tab": "Tab", "space": " ", "backspace": "Backspace", "delete": "Delete",
	"up": "ArrowUp", "down": "ArrowDown", "left": "ArrowLeft", "right": "ArrowRight",
	"home": "Home", "end": "End", "pgup": "PageUp", "pgdown": "PageDown", "insert": "Insert",
}

// LinkActivated runs click listeners on the anchor behind an nthtml LinkMsg.
// If no listener calls preventDefault, a NavigateMsg with the href resolved
// against the page base goes back to the host, which owns navigation.
func (l *Loop) LinkActivated(link document.Link) { l.LinkActivatedAt(0, link) }

// LinkActivatedAt rejects activation against a superseded DOM/frame.
func (l *Loop) LinkActivatedAt(gen uint64, link document.Link) {
	l.tryPost(func() {
		if l.page == nil || gen != 0 && (!l.FrameCurrent(gen) || l.page.frameGen != gen || l.page.Tree.Dirty()) {
			return
		}
		target := l.anchorFor(link)
		href := document.ResolveURL(l.page.Base, link.Href)
		if target != nil {
			href = anchorHref(l.page, target)
		}
		if href == "" {
			l.send(LogMsg{"warn", "link not openable: " + link.Href})
			return
		}
		var prevented bool
		if target != nil {
			var err error
			prevented, err = l.page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: target.ID(), Href: href})
			if !errors.Is(err, jsvm.ErrDisabled) {
				l.report(err) // a dropped runtime cannot prevent; the link just opens
			}
		}
		if target != nil {
			href = anchorHref(l.page, target)
		} // listeners may edit href
		if !prevented && href != "" {
			l.send(NavigateMsg{Href: href})
		}
	})
}

// ClickAt dispatches a click to the element under a document-pixel area (the
// host converts the terminal cell with app.cellToDocument; see app/cell.go). The
// target is the smallest painted box carrying an anchor name; anchors are
// element ids, including the synthetic ones the serializer mints for
// listener-bearing elements. Inline elements paint only a 1px marker, so a
// listener on a <span> is reached through its block ancestor; a link,
// control or label under the point wins (clickTarget). Focus follows the
// click; controls and labels get their activation behavior (forms.go); the
// nearest <a> supplies the default navigation.
func (l *Loop) ClickAt(area image.Rectangle) { l.ClickAtFrame(0, area) }

// ClickAtFrame keeps displayed CSS or chunk-local coordinates paired with their DOM/frame.
// HoverAtFrame updates CSS pointer state using the currently presented frame.
func (l *Loop) HoverAtFrame(gen uint64, area image.Rectangle) {
	l.tryPost(func() {
		p := l.page
		if p == nil || !l.FrameCurrent(gen) || p.frameGen != gen || p.Tree.Dirty() || l.cfg.Chunked || !p.result.HasHoverStyles() {
			return
		}
		id := ""
		if !area.Empty() && p.result != nil {
			id, _ = p.result.SourceAt(area.Min.Add(area.Size().Div(2)))
		}
		if id == p.hoverSource {
			return
		}
		p.hoverSource = id
		l.scheduleFrame()
	})
}

func (l *Loop) ClickAtFrame(gen uint64, area image.Rectangle) {
	l.tryPost(func() {
		if l.page == nil || gen != 0 && (!l.FrameCurrent(gen) || l.page.frameGen != gen || l.page.Tree.Dirty()) {
			return
		}
		if l.clickFrame(area) {
			return
		}
		l.page.frameFocus = 0
		l.announceFrameFocus()
		var best *document.Box
		for i := range l.page.boxes {
			b := &l.page.boxes[i]
			if b.Anchor == "" || !b.Bounds.Overlaps(area) {
				continue
			}
			if best == nil || b.Bounds.Dx()*b.Bounds.Dy() < best.Bounds.Dx()*best.Bounds.Dy() {
				best = b
			}
		}
		target := l.clickTarget(l.page, area)
		if target == nil && best != nil {
			target = l.page.Tree.ByID(best.Anchor)
		}
		if target == nil {
			return
		}
		if l.clickControl(l.page, target) {
			return
		}
		if dom.Focusable(target) {
			l.userFocus(l.page, target)
		} else {
			l.userFocus(l.page, nil)
		}
		href := ""
		var anchor *dom.Node
		for n := target; n != nil; n = n.Parent() {
			if n.Tag() == "a" {
				anchor = n
				href = anchorHref(l.page, n)
				break
			}
		}
		prevented, err := l.page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: target.ID(), Href: href})
		if !errors.Is(err, jsvm.ErrDisabled) {
			l.report(err)
		}
		if anchor != nil {
			href = anchorHref(l.page, anchor)
		}
		if !prevented && href != "" {
			l.send(NavigateMsg{Href: href})
		}
	})
}

// anchorFor maps an nthtml Link back to the DOM anchor. nthtml identifies
// links by a stable ID in document order over *visible, safe* anchors; the
// tree cannot compute visibility, so match on href+text and take the first
// candidate. (A Link.Anchor field in nthtml would make this exact; §11.)
func (l *Loop) anchorFor(link document.Link) *dom.Node {
	// nthtml reports Href as document.SafeURL(attr) and Text as the cleaned,
	// whitespace-collapsed label (alt text included), or the href itself when
	// the label is empty (<a href><img></a>). Compare in its terms, href
	// first; text only disambiguates duplicates. A Link.Anchor field in
	// nthtml would make this exact (design §11).
	anchors, _ := l.page.Tree.Query(l.page.Tree.Root(), "a", true)
	var byHref []*dom.Node
	for _, a := range anchors {
		if l.page.chunkInfo.Count > 0 {
			// Equal href/labels elsewhere in the DOM must never steal a click
			// from the displayed chunk. Unmapped anchors cannot receive it.
			if !l.page.chunkContains(dom.AnchorID(l.page.Tree, a)) {
				continue
			}
		}
		if raw, ok := a.Attr("href"); ok && document.SafeURL(raw) == link.Href {
			byHref = append(byHref, a)
		}
	}
	switch len(byHref) {
	case 0:
		return nil
	case 1:
		return byHref[0]
	}
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	want := norm(link.Text)
	for _, a := range byHref {
		if txt := norm(dom.TextContent(a)); txt == want || (txt == "" && link.Text == link.Href) {
			return a
		}
	}
	return byHref[0]
}

// Resize coalesces the latest width outside the input inbox and forces reflow.
func (l *Loop) Resize(widthPx int) {
	if widthPx <= 0 {
		return
	}
	l.resizeWidth.Store(int64(widthPx))
	select {
	case l.resizeWake <- struct{}{}:
	default:
	}
}

// Dropped reports how many inputs/log lines were shed under back-pressure.
func (l *Loop) Dropped() int64 { return l.dropped.Load() }

// ---- frame scheduler ----

func (l *Loop) afterTask() {
	l.armTimer()
	if l.page == nil {
		return
	}
	if l.page.Sandbox.Disabled() && !l.page.killed {
		l.dropPage(l.page) // heap or strike budget: free the runtime, keep the last frame
	}
	// Script-requested submissions and resets, bounded: a listener chain
	// that keeps queueing more is cut off instead of holding the loop.
	for passes := 0; len(l.page.formTasks) > 0 && !l.page.killed; passes++ {
		if passes == maxFormPasses {
			l.page.formTasks = nil
			l.send(LogMsg{"warn", "form submissions from script dropped: too many in one turn"})
			break
		}
		tasks := l.page.formTasks
		l.page.formTasks = nil
		for _, fn := range tasks {
			fn()
		}
	}
	if !l.page.killed && l.cfg.Profile.ScriptsOn() {
		l.report(l.page.Sandbox.FlushMutations())
	}
	l.reconcileFrames()
	if l.page.scrollEvent && !l.page.killed {
		l.page.scrollEvent = false
		if l.cfg.Profile.ScriptsOn() {
			_, err := l.page.Sandbox.Dispatch(jsvm.Event{Type: "scroll", NonCancelable: true})
			l.report(err)
		}
	}
	l.updateImages()
	l.completeDocument()
	if l.page.Sandbox.Disabled() && !l.page.killed {
		l.dropPage(l.page)
	}
	l.armTimer() // image handlers may have scheduled timers
	if p := l.page; p.pendingLocation != nil {
		v := *p.pendingLocation
		p.pendingLocation = nil
		if !p.killed {
			p.locationRequests++
			l.send(v)
		}
	}
	l.reconcileFocus(l.page)
	if l.page.Tree.Dirty() {
		l.currentFrame.Store(0)
	}
	if l.page.Tree.Dirty() || l.pendingFrame {
		l.scheduleFrame()
	}
}

func (l *Loop) scheduleFrame() {
	if l.page == nil {
		return
	}
	if l.renderInFlight {
		l.pendingFrame = true // coalesce: one render at a time, latest tree wins
		return
	}
	if wait := l.cfg.MinFrame - time.Since(l.lastFrame); wait > 0 {
		l.frameTimer.Reset(wait)
		return
	}
	l.startRender()
}

func (l *Loop) startRender() {
	if l.page == nil || l.renderInFlight {
		return
	}
	if wait := l.cfg.MinFrame - time.Since(l.lastFrame); wait > 0 {
		l.frameTimer.Reset(wait)
		return
	}
	l.lastFrame = time.Now()
	if !l.page.killed && l.cfg.Profile.ScriptsOn() {
		l.report(l.page.Sandbox.FlushMutations())
	}
	l.runFrameCallbacks()
	if l.page.Sandbox.Disabled() && !l.page.killed {
		l.dropPage(l.page)
	}
	page := l.page
	opts := document.Options{HoverSource: page.hoverSource, DisableDiagrams: page.diagramsDisabled, ViewportHeight: l.cfg.FrameHeight, NoImage: !l.cfg.Chunked, Width: l.cfg.Width, Limits: l.cfg.RenderLimit, Fonts: l.cfg.Fonts, FallbackFaces: fallbackFaces}
	var loaderWarnings []string
	if page.diagramsDisabled {
		loaderWarnings = append(loaderWarnings, diagramFallbackWarning)
	}
	var injected []string // hrefs whose sheet is in this snapshot
	if page.subres != nil {
		// Discover before the gate so a <link> a script added is in the
		// blocking set while the first frame is still unrendered.
		page.subres.Discover(page.Tree.LinkedStyles(), page.Tree.ImageSources(), page.backgrounds)
		if page.firstFrame && page.subres.StylesPending() && !page.styleDeadline {
			l.pendingFrame = true // the last settlement or the deadline re-schedules
			return
		}
		loaderWarnings, injected = l.injectStyles(page)
		opts.Images = page.subres.Images()                     // fresh copies: nothing the
		opts.BackgroundImages = page.subres.BackgroundImages() // worker reads is written again
	}
	frameHTML, frameImages := l.frameImages(page, &opts)
	requestedScroll := page.frameScroll
	rebuildChunks := page.Tree.Dirty()
	if rebuildChunks {
		l.currentFrame.Store(0)
	}
	chunkState, chunkReq := page.chunkState, page.chunkReq
	useChunks := l.cfg.Chunked
	var html string
	if !useChunks || chunkState == nil || rebuildChunks {
		html = page.Tree.SerializeFrames(frameHTML) // immutable snapshot after injection
	}
	page.Tree.ClearDirty()
	l.pendingFrame = len(l.frames) > 0
	l.renderInFlight = true
	l.lastFrame = time.Now()
	gen := l.gen.Add(1)
	navigation := l.navGen.Load()
	l.cfg.Trace.Record("snapshot", map[string]any{"url": page.Base, "navigation": navigation, "frame": gen, "html": html, "scripts": len(page.Tree.Scripts), "loaded": page.loaded, "disabled": page.killed, "timers": len(l.timers), "animation_frames": len(l.frames), "dropped": l.dropped.Load()})
	viewport := l.viewport()
	if page.firstFrame {
		viewport.Origin = image.Point{}
		viewport.ZoomSteps = 0
	}
	l.renderJobs <- func(worker *paintWorker) {
		ctx, cancel := context.WithTimeout(page.paintCtx, paintDeadline)
		defer cancel()
		// Parse and Render enforce nthtml's own byte/node/pixel budgets; a
		// hostile tree fails here with document.ErrLimit instead of
		// exhausting memory. One parse per frame also yields the CSS
		// background sources (decision 21).
		var res *document.Result
		var painted *nthtml.PaintedResult
		var backgrounds []string
		var chunkInfo ChunkInfo
		if useChunks {
			chunkInfo.Count = 1
		}
		var doc *document.Document
		var err error
		if useChunks && chunkState != nil && !rebuildChunks {
			doc = chunkState.doc
		} else {
			doc, err = document.Parse(ctx, strings.NewReader(html), opts.Limits)
		}
		if err == nil && doc != nil {
			for _, src := range doc.ImageSourceList() {
				if src.Kind == "background" && !strings.HasPrefix(strings.ToLower(src.Src), "data:") {
					backgrounds = append(backgrounds, src.Src)
				}
			}
			renderDocument := func() {
				if useChunks {
					if chunkState == nil || rebuildChunks || chunkState.width != opts.Width || chunkState.diagramsDisabled != opts.DisableDiagrams {
						old := chunkState
						chunkState, err = newChunkRender(ctx, doc, opts)
						if err == nil && old != nil && chunkReq.index == old.index && chunkReq.anchor == "" && old.place != "" {
							if start, end, ok := chunkState.source.ChunkRange(old.place); ok && (chunkReq.index < start || chunkReq.index >= end) {
								chunkReq.index = start
							}
						}
					}
					if err == nil {
						res, chunkInfo, err = chunkState.render(ctx, chunkReq, opts)
					}
				} else {
					res, err = doc.Render(ctx, opts)
				}
			}
			renderDocument()
			if !opts.DisableDiagrams && diagramBudgetExceeded(err) && ctx.Err() == nil {
				// Keep the same document, CSS, images, deadline and budgets.
				// nthtml's supported label/source fallback avoids rasterizing
				// diagrams; it does not grant a larger resource allowance.
				opts.DisableDiagrams = true
				renderDocument()
				loaderWarnings = append(loaderWarnings, diagramFallbackWarning)
			}
			if !useChunks && errors.Is(err, document.ErrLimit) && (opts.Images != nil || opts.BackgroundImages != nil) {
				// Images are the only thing the host added: drop them and
				// retry once so an image-heavy page degrades to placeholders
				// instead of a blank viewer (decision 16).
				opts.Images, opts.BackgroundImages = nil, nil
				frameImages = nil
				res, err = doc.Render(ctx, opts)
				loaderWarnings = append(loaderWarnings, "page too large with images; rendered without them")
			}
		}
		var embeddedImage image.Image
		embeddedOrigin := requestedScroll
		if l.cfg.FrameHeight > 0 && err == nil && res != nil {
			embeddedOrigin.X = max(0, min(embeddedOrigin.X, res.Bounds().Dx()-opts.Width))
			embeddedOrigin.Y = max(0, min(embeddedOrigin.Y, res.Bounds().Dy()-l.cfg.FrameHeight))
			embeddedImage, err = res.Paint(ctx, image.Rect(0, 0, opts.Width, l.cfg.FrameHeight).Add(embeddedOrigin), 1)
			if err == nil && embeddedImage.Bounds().Size() != image.Pt(opts.Width, l.cfg.FrameHeight) {
				canvas := image.NewRGBA(image.Rect(0, 0, opts.Width, l.cfg.FrameHeight))
				draw.Draw(canvas, canvas.Bounds(), image.NewUniform(res.Background()), image.Point{}, draw.Src)
				draw.Draw(canvas, embeddedImage.Bounds(), embeddedImage, image.Point{}, draw.Src)
				embeddedImage = canvas
			}
		}
		if l.cfg.FrameHeight == 0 && !useChunks && err == nil && res != nil {
			var painter *document.Painter
			var plan nthtml.PaintPlan
			painter, err = res.NewPainter(ctx)
			if err == nil {
				plan, err = nthtml.PlanPaint(res, viewport)
				if err == nil {
					var band *image.RGBA
					band, err = painter.Paint(ctx, plan.Area, plan.Scale)
					if err == nil {
						painted = &nthtml.PaintedResult{Result: res, Revision: gen, Plan: plan, Image: band}
						worker.close()
						worker.painter, worker.result, worker.revision, worker.ctx = painter, res, gen, page.paintCtx
					}
				}
				if err != nil {
					painter.Close()
				}
			}
		}
		if useChunks && err != nil && doc == nil {
			chunkState = nil
		}
		l.post(func() {
			l.renderInFlight = false
			if l.page != page || navigation != l.navGen.Load() {
				return // frame for a page we navigated away from
			}
			if opts.DisableDiagrams {
				page.diagramsDisabled = true
			}
			if opts.Width != l.cfg.Width {
				l.currentFrame.Store(0)
				l.pendingFrame = true
				return
			}
			if useChunks {
				page.chunkState = chunkState
				// A newer chunk request supersedes this frame. Finish the worker
				// before starting another: the engine is never used concurrently.
				if page.chunkReq.seq != chunkReq.seq {
					l.pendingFrame = true
					return
				}
				page.chunkIndex = nil
				if chunkState != nil {
					page.chunkIndex = chunkState.source.ChunkSource
				}
				if chunkInfo.Count > 0 {
					page.chunkInfo = chunkInfo
					page.chunkReq.index = chunkInfo.Index
				}
				page.chunkReq.anchor, page.chunkReq.bottom = "", false
			}
			if !useChunks && errors.Is(err, document.ErrLimit) && len(injected) > 0 {
				// nthtml refused a page that only differs from the one it
				// would accept by the injected sheets: drop them for good and
				// render without, rather than show a failed frame forever.
				page.Tree.ClearStyles()
				for _, href := range injected {
					page.refused[href] = true
				}
				l.send(LogMsg{"error", fmt.Sprintf("stylesheets dropped, the renderer refused the page with them: %v (%s)", injected, err)})
				page.Tree.MarkDirty()
				return
			}
			if useChunks && err != nil {
				page.boxes, page.result = nil, nil
			}
			if res != nil && err == nil {
				page.boxes, page.result = res.Boxes, res
				page.displayedFrames = frameImages
				if l.cfg.FrameHeight > 0 && page.frameScroll == requestedScroll {
					if page.frameScroll != embeddedOrigin {
						page.scrollEvent = true
					}
					page.frameScroll = embeddedOrigin
				}
				l.autofocus(page)
			}
			if page.subres != nil {
				page.backgrounds = backgrounds
				page.subres.Discover(nil, nil, backgrounds)
				if page.firstFrame {
					page.firstFrame = false
					page.subres.FirstFrameRendered()
				}
				loaderWarnings = append(loaderWarnings, page.subres.Warnings()...)
			}
			if err == nil || useChunks {
				page.frameGen = gen
				if !page.Tree.Dirty() {
					l.currentFrame.Store(gen)
					l.frameNavigation.Store(navigation)
				}
				page.firstFrame = false
			}
			l.send(FrameMsg{FrameImage: embeddedImage, FrameOrigin: embeddedOrigin, FrameRegions: l.frameRegions(page), Painted: painted, Navigation: navigation, Gen: gen, Result: res, Err: err, Warnings: loaderWarnings, Chunk: chunkInfo})
		})
	}
}

// startSubresources opens the page's subresource session and arms the
// first-frame deadline. Completions are posted through a wrapper that drops
// them after navigation and marks the tree dirty so a frame shows them
// (spec §7.5).
func (l *Loop) startSubresources(page *Page) {
	cfg := l.cfg.Subresources
	if !l.cfg.Profile.AllowsSubresources() || l.cfg.SubresourceTransport == nil || cfg.Mode == subres.ModeOff {
		return
	}
	post := func(fn func()) {
		l.post(func() {
			if l.page != page {
				return
			}
			fn()
			page.Tree.MarkDirty()
		})
	}
	page.subres = subres.NewSession(page.ctx, cfg, l.cfg.SubresourceTransport(page.Base), page.Base, post)
	page.subres.Discover(page.Tree.LinkedStyles(), page.Tree.ImageSources(), nil)
	time.AfterFunc(cfg.StyleTimeout, func() {
		l.post(func() {
			if l.page != page {
				return
			}
			page.styleDeadline = true
			if page.firstFrame {
				l.scheduleFrame() // only the frame the gate is holding; a quiet page gets no extra render
			}
		})
	})
}

// injectStyles charges the rule budget in document order at every render
// (decision 6): inline <style> elements present now count first, then each
// loaded external sheet is injected if it fits and removed if it does not.
func (l *Loop) injectStyles(page *Page) (warnings, injected []string) {
	budget := l.cfg.RenderLimit.Rules
	for _, text := range page.Tree.StyleTexts() {
		budget -= subres.CountRules(text)
	}
	for _, href := range page.Tree.LinkedStyles() {
		if page.refused[href] {
			continue
		}
		css, n, ok := page.subres.Style(href)
		if !ok {
			continue
		}
		if n > budget {
			_ = page.Tree.SetStyle(href, "")
			if !page.budgetWarned[href] {
				page.budgetWarned[href] = true
				warnings = append(warnings, fmt.Sprintf("stylesheet %q dropped: over the rule budget", href))
			}
			continue
		}
		if err := page.Tree.SetStyle(href, css); err != nil {
			warnings = append(warnings, fmt.Sprintf("stylesheet %q dropped: %v", href, err))
			continue
		}
		budget -= n
		injected = append(injected, href)
	}
	return warnings, injected
}

type frameCB struct {
	id int
	fn goja.Callable
}

// runFrameCallbacks snapshots the batch before rendering. Newly registered
// callbacks wait for another frame; cancellation can still remove a callback
// from the active batch. Unstarted callbacks survive budget exhaustion.
func (l *Loop) runFrameCallbacks() {
	if len(l.frames) == 0 || l.page == nil || l.page.killed {
		return
	}
	l.activeFrames = l.frames
	l.frames = nil
	sb := l.page.Sandbox
	ts := sb.PerformanceNow()
	deadline := time.Now().Add(time.Duration(l.cfg.Profile.FrameBudget()) * time.Millisecond)
	for i := range l.activeFrames {
		cb := l.activeFrames[i]
		if cb.fn == nil {
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		l.activeFrames[i].fn = nil
		l.report(sb.CallWithinBudget(remaining, cb.fn, goja.Undefined(), sb.ToValue(ts)))
		if sb.Disabled() {
			break
		}
	}
	pending := make([]frameCB, 0, len(l.activeFrames)+len(l.frames))
	if !sb.Disabled() {
		for _, cb := range l.activeFrames {
			if cb.fn != nil {
				pending = append(pending, cb)
			}
		}
		pending = append(pending, l.frames...)
	}
	l.activeFrames = nil
	l.frames = pending
}

// ---- timers ----

type timer struct {
	internal bool
	id       int
	when     time.Time
	interval time.Duration
	repeat   bool
	fn       goja.Callable
	args     []goja.Value
	index    int
}

type timerHeap []*timer

func (h timerHeap) Len() int           { return len(h) }
func (h timerHeap) Less(i, j int) bool { return h[i].when.Before(h[j].when) }
func (h timerHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index, h[j].index = i, j }
func (h *timerHeap) Push(x any)        { t := x.(*timer); t.index = len(*h); *h = append(*h, t) }
func (h *timerHeap) Pop() any          { old := *h; t := old[len(old)-1]; *h = old[:len(old)-1]; return t }
func (h timerHeap) find(id int) *timer {
	for _, t := range h {
		if t.id == id {
			return t
		}
	}
	return nil
}

// fireTimers runs every due timer, each in its own watchdog slice, so one
// slow callback cannot starve the others or the frame.
func (l *Loop) fireTimers() {
	now := time.Now()
	for len(l.timers) > 0 && !l.timers[0].when.After(now) {
		t := heap.Pop(&l.timers).(*timer)
		if t.repeat {
			t.when = now.Add(t.interval)
			heap.Push(&l.timers, t)
		}
		if l.page == nil {
			continue
		}
		err := l.page.Sandbox.Call(t.fn, goja.Undefined(), t.args...)
		if errors.Is(err, jsvm.ErrDisabled) {
			l.timers = nil
			return
		}
		l.report(err)
	}
}

// ---- jsvm.Host implementation (called on the VM goroutine) ----

type host Loop

func (h *host) SetTimer(delay time.Duration, repeat bool, fn goja.Callable, args []goja.Value) (int, bool) {
	l := (*Loop)(h)
	limit := l.cfg.Script.MaxTimers
	if limit <= 0 {
		limit = 256
	}
	if len(l.timers) >= limit {
		return 0, false
	}
	l.nextID++
	heap.Push(&l.timers, &timer{id: l.nextID, when: time.Now().Add(delay), interval: delay, repeat: repeat, fn: fn, args: args})
	return l.nextID, true
}

func (h *host) ClearTimer(id int) {
	l := (*Loop)(h)
	if t := l.timers.find(id); t != nil && !t.internal {
		heap.Remove(&l.timers, t.index)
	}
}

// QueueTask uses the bounded timer scheduler without exposing its cancellation
// handle to clearTimeout/clearInterval. Navigation clears both task classes.
func (h *host) QueueTask(fn goja.Callable) (int, bool) {
	id, ok := h.SetTimer(0, false, fn, nil)
	if ok {
		(*Loop)(h).timers.find(id).internal = true
	}
	return id, ok
}
func (h *host) ClearTask(id int) {
	l := (*Loop)(h)
	if t := l.timers.find(id); t != nil && t.internal {
		heap.Remove(&l.timers, t.index)
	}
}

// RequestFrame schedules a frame even when the tree is clean, so a page that
// only animates through rAF (reading state, drawing later) still gets its
// callback at the frame cadence.
func (h *host) RequestFrame(fn goja.Callable) int {
	l := (*Loop)(h)
	count := len(l.frames)
	for _, cb := range l.activeFrames {
		if cb.fn != nil {
			count++
		}
	}
	if count >= l.cfg.Profile.FrameCallbackLimit() {
		return 0
	}
	l.nextID++
	l.frames = append(l.frames, frameCB{id: l.nextID, fn: fn})
	l.pendingFrame = true
	return l.nextID
}

func (h *host) CancelFrame(id int) {
	l := (*Loop)(h)
	for i, cb := range l.activeFrames {
		if cb.id == id {
			l.activeFrames[i].fn = nil
			return
		}
	}
	for i, f := range l.frames {
		if f.id == id {
			l.frames = append(l.frames[:i], l.frames[i+1:]...)
			return
		}
	}
}

// Fetch runs the request on a worker and posts the completion back to the VM
// goroutine, which is the only place the Promise may be settled.
func (h *host) Fetch(req jsvm.FetchRequest, done func(jsvm.FetchResponse, error)) error {
	l := (*Loop)(h)
	page := l.page
	if page == nil {
		return errors.New("no page")
	}
	go func() {
		var resp netpolicy.Response
		var err error
		if f := l.cfg.Profile.Fetch; f == profile.FetchOrigin || f == profile.FetchAllowlist {
			resp, err = page.Fetcher.Do(page.ctx, netpolicy.Request{OmitCookies: req.OmitCookies, Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body})
		} else {
			err = fmt.Errorf("%w: fetch is off", netpolicy.ErrDenied)
			l.cfg.Trace.Record("network_error", map[string]string{"url": req.URL, "error": err.Error()})
		}
		l.post(func() {
			if l.page != page {
				return // promise belongs to a discarded runtime; let it be collected
			}
			done(jsvm.FetchResponse{Status: resp.Status, StatusText: resp.StatusText, URL: resp.URL, Headers: resp.Headers, Body: resp.Body}, err)
		})
	}()
	return nil
}

func (h *host) Log(level, line string) { (*Loop)(h).send(LogMsg{Level: level, Line: line}) }

func (l *Loop) evalScript(page *Page, name, source string) {
	err := page.Sandbox.Eval(name, source)
	result := "ok"
	if err != nil {
		result = err.Error()
	}
	l.cfg.Trace.Record("script_result", map[string]string{"url": name, "result": result})
	if errors.Is(err, jsvm.ErrModuleSyntax) {
		// Module scripts are skipped by type; a classic bootstrap that reaches
		// for import() is the same gap and gets the same quiet notice.
		l.send(LogMsg{"warn", fmt.Sprintf("script skipped: %s uses import(), and ES modules are not supported", name)})
		return
	}
	l.report(err)
}
