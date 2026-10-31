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
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/dop251/goja"
	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/subres"
)

// Sink is satisfied by *tea.Program. It may block; the loop never calls it
// from the VM goroutine directly.
type Sink interface{ Send(tea.Msg) }

// Messages the loop sends to the Bubble Tea model.
type (
	FrameMsg struct {
		Gen    uint64 // monotonically increasing; stale frames are dropped by the host
		Result *document.Result
		Err    error
		// Warnings are the loader's (a refused stylesheet, an oversize
		// image); Result.Warnings stay nthtml's. Page-controlled text:
		// the host passes them through app.clean.
		Warnings []string
	}
	// NavigateMsg carries an absolute URL already resolved against the page
	// base. Admission (scheme, host) is the host's decision.
	// Form marks a form submission (the host words its confirmation
	// differently; the admission gate is the same as for links).
	NavigateMsg struct {
		Href string
		Form bool
	}
	LogMsg   struct{ Level, Line string }
	TitleMsg struct{ Title string }
)

// Page is one navigation: tree + sandbox + policy. Replacing it discards the
// whole JS heap, which is how cross-page isolation is achieved.
type Page struct {
	Tree    *dom.Tree
	Sandbox *jsvm.Sandbox
	Fetcher *netpolicy.Fetcher
	Base    string
	started time.Time
	loaded  bool
	// ctx bounds every request this page starts; cancel fires when the page
	// is replaced or killed, so no completion outlives its runtime.
	ctx    context.Context
	cancel context.CancelFunc
	killed bool // runtime dropped (heap or strike budget); the last frame stays
	// boxes are the painted boxes of the last frame shown for this page,
	// used to map a document-pixel hit back to an element (Box.Anchor = id).
	boxes []document.Box
	// result is the last frame's Result (SourceAt refines clicks, forms.go).
	result *document.Result
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
	cfg    Config
	sink   Sink
	inbox  chan func()
	outbox chan tea.Msg
	ctx    context.Context

	page   *Page
	timers timerHeap
	nextID int
	frames []frameCB

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
	if cfg.Width <= 0 {
		cfg.Width = 800
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
	if cfg.RenderLimit.Rules <= 0 || cfg.RenderLimit.Rules > 4096 {
		cfg.RenderLimit.Rules = 4096 // nthtml's ceiling (it refuses larger values): every sheet in the measured sample fits (spec decision 6)
	}
	cfg.Subresources = cfg.Subresources.WithDefaults()
	l := &Loop{ctx: ctx, cfg: cfg, sink: sink, inbox: make(chan func(), cfg.Inbox), outbox: make(chan tea.Msg, cfg.Outbox), timerT: stoppedTimer(), frameTimer: stoppedTimer()}
	go l.forward()
	return l
}

// Run is the VM goroutine. It is the only goroutine that may touch l.page.
func (l *Loop) Run() {
	for {
		select {
		case <-l.ctx.Done():
			return
		case task := <-l.inbox:
			task()
		case <-l.timerT.C:
			l.fireTimers()
		case <-l.frameTimer.C:
			l.startRender()
		}
		l.afterTask()
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
func (l *Loop) LoadPage(html string, base string, fetcher *netpolicy.Fetcher) {
	gen := l.navGen.Add(1) // taken synchronously on the caller: call order == generation order
	if fetcher == nil {
		fetcher = netpolicy.New(netpolicy.Policy{}) // the zero policy denies everything; never a nil dereference on a worker
	}
	task := func() {
		if gen != l.navGen.Load() {
			return // a newer LoadPage exists; whichever order the handoffs ran in, it wins
		}
		if l.page != nil {
			l.dropPage(l.page)
		}
		l.timers = nil
		l.frames = nil
		tree, err := dom.Parse(strings.NewReader(html), dom.Limits{})
		if err != nil {
			l.page = nil
			l.send(LogMsg{"error", "parse: " + err.Error()})
			return
		}
		cfg := l.cfg.Script
		cfg.Location = base
		if ms := l.cfg.Profile.ClockMillis; ms > 0 && cfg.Clock == nil {
			res := time.Duration(ms) * time.Millisecond
			cfg.Clock = func() time.Time { return time.Now().Truncate(res) }
		}
		page := &Page{Tree: tree, Fetcher: fetcher, Base: base, started: time.Now(), firstFrame: true, budgetWarned: map[string]bool{}, refused: map[string]bool{}, firing: map[dom.NodeID]bool{}, resetting: map[dom.NodeID]bool{}}
		page.ctx, page.cancel = context.WithCancel(l.ctx)
		page.Sandbox = jsvm.New(cfg, tree, (*host)(l))
		page.Sandbox.SetFormHooks(l.formHooks(page))
		l.page = page
		l.startSubresources(page)
		l.send(TitleMsg{tree.Title()})
		l.runScripts(page, 0)
		l.scheduleFrame()
	}
	// Never block the UI goroutine: the inbox normally has room; if it is
	// full, hand off in a goroutine. Ordering is irrelevant because of gen.
	if !l.tryPost(task) {
		go l.post(task)
	}
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
	if page.subres != nil {
		page.subres.Close()
	}
	page.Sandbox.Close()
	if page.cancel != nil {
		page.cancel()
	}
	l.timers = nil
	l.frames = nil
	runtime.GC()
}

// runScripts executes tree.Scripts[i:] in order. An external script suspends
// the sequence until its body arrives, preserving document order; the
// sequence resumes on the VM goroutine.
func (l *Loop) runScripts(page *Page, i int) {
	if !l.cfg.Profile.ScriptsOn() {
		return // the scripts gate: not even DOMContentLoaded
	}
	for ; i < len(page.Tree.Scripts); i++ {
		sc := page.Tree.Scripts[i]
		switch {
		case sc.Inline != "":
			l.report(page.Sandbox.Eval(fmt.Sprintf("%s#inline-%d", page.Base, i), sc.Inline))
		case sc.Src != "" && l.cfg.Profile.Scripts == profile.ScriptsExternal:
			next := i + 1
			go func() {
				resp, err := page.Fetcher.Do(page.ctx, netpolicy.Request{Method: "GET", URL: sc.Src})
				l.post(func() {
					if l.page != page {
						return // navigated away
					}
					if err != nil {
						l.report(err)
					} else {
						l.report(page.Sandbox.Eval(sc.Src, resp.Body))
					}
					l.runScripts(page, next)
				})
			}()
			return
		}
	}
	if !page.loaded {
		page.loaded = true
		_, err := page.Sandbox.Dispatch(jsvm.Event{Type: "DOMContentLoaded"})
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
func (l *Loop) Input(key tea.KeyPressMsg) {
	l.tryPost(func() {
		if l.page == nil || !l.page.Sandbox.HasListeners("keydown") {
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
func (l *Loop) LinkActivated(link document.Link) {
	l.tryPost(func() {
		if l.page == nil {
			return
		}
		href := document.ResolveURL(l.page.Base, link.Href)
		if href == "" {
			l.send(LogMsg{"warn", "link not openable: " + link.Href})
			return
		}
		var prevented bool
		if target := l.anchorFor(link); target != nil {
			var err error
			prevented, err = l.page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: target.ID(), Href: href})
			if !errors.Is(err, jsvm.ErrDisabled) {
				l.report(err) // a dropped runtime cannot prevent; the link just opens
			}
		}
		if !prevented {
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
func (l *Loop) ClickAt(area image.Rectangle) {
	l.tryPost(func() {
		if l.page == nil {
			return
		}
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
		for n := target; n != nil; n = n.Parent() {
			if n.Tag() == "a" {
				if raw, ok := n.Attr("href"); ok {
					href = document.ResolveURL(l.page.Base, raw)
				}
				break
			}
		}
		prevented, err := l.page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: target.ID(), Href: href})
		if !errors.Is(err, jsvm.ErrDisabled) {
			l.report(err)
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

// Resize changes the layout width and forces a re-flow of the current tree.
func (l *Loop) Resize(widthPx int) {
	l.tryPost(func() {
		if widthPx > 0 && widthPx != l.cfg.Width {
			l.cfg.Width = widthPx
			l.pendingFrame = true
			l.scheduleFrame()
		}
	})
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
	l.reconcileFocus(l.page)
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
	page := l.page
	opts := document.Options{Width: l.cfg.Width, Limits: l.cfg.RenderLimit, Fonts: l.cfg.Fonts, FallbackFaces: fallbackFaces}
	var loaderWarnings []string
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
	html := page.Tree.Serialize() // immutable snapshot, after injection; safe to hand off
	page.Tree.ClearDirty()
	l.pendingFrame = false
	l.renderInFlight = true
	l.lastFrame = time.Now()
	gen := l.gen.Add(1)
	go func() {
		ctx, cancel := context.WithTimeout(l.ctx, 2*time.Second)
		defer cancel()
		// Parse and Render enforce nthtml's own byte/node/pixel budgets; a
		// hostile tree fails here with document.ErrLimit instead of
		// exhausting memory. One parse per frame also yields the CSS
		// background sources (decision 21).
		var res *document.Result
		var backgrounds []string
		doc, err := document.Parse(ctx, strings.NewReader(html), opts.Limits)
		if err == nil && doc != nil {
			for _, src := range doc.ImageSourceList() {
				if src.Kind == "background" && !strings.HasPrefix(strings.ToLower(src.Src), "data:") {
					backgrounds = append(backgrounds, src.Src)
				}
			}
			res, err = doc.Render(ctx, opts)
			if errors.Is(err, document.ErrLimit) && (opts.Images != nil || opts.BackgroundImages != nil) {
				// Images are the only thing the host added: drop them and
				// retry once so an image-heavy page degrades to placeholders
				// instead of a blank viewer (decision 16).
				opts.Images, opts.BackgroundImages = nil, nil
				res, err = doc.Render(ctx, opts)
				loaderWarnings = append(loaderWarnings, "page too large with images; rendered without them")
			}
		}
		l.post(func() {
			l.renderInFlight = false
			if l.page != page {
				return // frame for a page we navigated away from
			}
			if errors.Is(err, document.ErrLimit) && len(injected) > 0 {
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
			if res != nil {
				page.boxes, page.result = res.Boxes, res
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
			l.send(FrameMsg{Gen: gen, Result: res, Err: err, Warnings: loaderWarnings})
			l.runFrameCallbacks()
		})
	}()
}

// startSubresources opens the page's subresource session and arms the
// first-frame deadline. Completions are posted through a wrapper that drops
// them after navigation and marks the tree dirty so a frame shows them
// (spec §7.5).
func (l *Loop) startSubresources(page *Page) {
	cfg := l.cfg.Subresources
	if l.cfg.SubresourceTransport == nil || cfg.Mode == subres.ModeOff {
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

// runFrameCallbacks delivers requestAnimationFrame callbacks after a frame
// was produced, each under its own slice, with a monotonic millisecond
// timestamp since page start (DOMHighResTimeStamp semantics, 10 ms coarse).
func (l *Loop) runFrameCallbacks() {
	if len(l.frames) == 0 || l.page == nil {
		return
	}
	cbs := l.frames
	l.frames = nil
	sb := l.page.Sandbox
	ts := float64(time.Since(l.page.started).Truncate(10*time.Millisecond)) / float64(time.Millisecond)
	for _, cb := range cbs {
		l.report(sb.Call(cb.fn, goja.Undefined(), sb.ToValue(ts)))
	}
}

// ---- timers ----

type timer struct {
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
	if t := l.timers.find(id); t != nil {
		heap.Remove(&l.timers, t.index)
	}
}

// RequestFrame schedules a frame even when the tree is clean, so a page that
// only animates through rAF (reading state, drawing later) still gets its
// callback at the frame cadence.
func (h *host) RequestFrame(fn goja.Callable) int {
	l := (*Loop)(h)
	if len(l.frames) >= 256 {
		return 0
	}
	l.nextID++
	l.frames = append(l.frames, frameCB{id: l.nextID, fn: fn})
	l.pendingFrame = true
	return l.nextID
}

func (h *host) CancelFrame(id int) {
	l := (*Loop)(h)
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
			resp, err = page.Fetcher.Do(page.ctx, netpolicy.Request{Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body})
		} else {
			err = fmt.Errorf("%w: fetch is off", netpolicy.ErrDenied)
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
