// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"fmt"
	"image"
	"mime"
	"net/url"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/origin"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/subres"
	"nimbleterminal.dev/nthtml/document"
)

const maxLiveFrames = 4
const maxFrameLoads = 64
const maxFrameDepth = 2

type frameEndpoint struct {
	loop     *Loop
	revision uint64
	origin   string
}
type frameHub struct {
	sync.Mutex
	next           uint64
	loads          int
	endpoints      map[uint64]frameEndpoint
	pending, bytes int
}

func (h *frameHub) allocate() (uint64, error) {
	h.Lock()
	defer h.Unlock()
	if len(h.endpoints) >= maxLiveFrames+1 || h.loads >= maxFrameLoads {
		return 0, fmt.Errorf("frame count limit reached")
	}
	h.next++
	h.loads++
	h.endpoints[h.next] = frameEndpoint{}
	return h.next, nil
}
func (h *frameHub) alive(id uint64) bool {
	h.Lock()
	defer h.Unlock()
	_, ok := h.endpoints[id]
	return ok
}
func (h *frameHub) remove(id uint64) { h.Lock(); delete(h.endpoints, id); h.Unlock() }
func (h *frameHub) bind(id uint64, l *Loop, o string) uint64 {
	h.Lock()
	defer h.Unlock()
	e, ok := h.endpoints[id]
	if !ok {
		return 0
	}
	e.revision++
	e.loop = l
	e.origin = o
	h.endpoints[id] = e
	return e.revision
}
func (h *frameHub) post(source, revision uint64, m jsvm.FramePost) error {
	h.Lock()
	src, ok := h.endpoints[source]
	dst, found := h.endpoints[m.Target]
	if !ok || !found || src.revision != revision || dst.loop == nil {
		h.Unlock()
		return nil
	}
	to := m.TargetOrigin
	if to == "/" {
		to = src.origin
		if to == "null" && source != m.Target {
			h.Unlock()
			return nil
		}
	}
	if to != "*" && (to == "null" || to != dst.origin) {
		h.Unlock()
		return nil
	}
	if h.pending >= 64 || len(m.Data) > (4<<20)-h.bytes {
		h.Unlock()
		return fmt.Errorf("frame message queue limit reached")
	}
	h.pending++
	h.bytes += len(m.Data)
	h.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { h.Lock(); h.pending--; h.bytes -= len(m.Data); h.Unlock() }) }
	stop := context.AfterFunc(dst.loop.ctx, release)
	deliver := func() {
		stop()
		release()
		h.Lock()
		s, sourceOK := h.endpoints[source]
		d, targetOK := h.endpoints[m.Target]
		valid := sourceOK && targetOK && s.revision == revision && d.revision == dst.revision && d.loop == dst.loop
		h.Unlock()
		if !valid || dst.loop.page == nil || dst.loop.page.killed {
			return
		}
		dst.loop.report(dst.loop.page.Sandbox.ReceiveFrameMessage(source, src.origin, m.Data))
	}
	if !dst.loop.tryPost(deliver) {
		stop()
		release()
		return fmt.Errorf("frame receiver busy")
	}
	return nil
}

type childFrame struct {
	offset     image.Point
	id         uint64
	spec       jsvm.FrameSpec
	revision   uint64
	cancel     context.CancelFunc
	child      *Loop
	image      image.Image
	generation uint64
	pending    bool
}
type framePresentation struct {
	offset     image.Point
	revision   uint64
	frame      *childFrame
	image      image.Image
	generation uint64
}

func (l *Loop) setupFrameRealm(page *Page, cfg *jsvm.Config) {
	if l.cfg.frameHub == nil && !l.cfg.Profile.FramesOn() {
		return
	}
	if l.cfg.frameHub == nil || l.cfg.frameID == 0 {
		l.cfg.frameHub = &frameHub{endpoints: map[uint64]frameEndpoint{}}
	}
	h := l.cfg.frameHub
	id := l.cfg.frameID
	if id == 0 {
		var err error
		id, err = h.allocate()
		if err != nil {
			l.report(err)
			return
		}
	}
	page.frameID = id
	page.frames = map[dom.NodeID]*childFrame{}
	ownOrigin, _ := origin.Of(page.Base)
	if ownOrigin == "" || l.cfg.frameOpaque {
		ownOrigin = "null"
	}
	revision := h.bind(id, l, ownOrigin)
	cfg.FrameDOM = func(r jsvm.DOMRequest) (jsvm.DOMReply, error) { return l.frameDOM(page, revision, r) }
	cfg.CloseFrameDOM = func() { l.releaseFrameDOM(page) }
	cfg.FrameSelf = id
	cfg.FrameParent = l.cfg.frameParent
	cfg.FrameTop = l.cfg.frameTop
	if cfg.FrameTop == 0 {
		cfg.FrameTop = id
	}
	cfg.FrameOpaque = l.cfg.frameOpaque
	cfg.FrameAlive = h.alive
	cfg.PostFrame = func(m jsvm.FramePost) error { return h.post(id, revision, m) }
	cfg.EnsureFrame = func(spec jsvm.FrameSpec) (uint64, error) { return l.ensureChildFrame(page, spec) }
}
func (l *Loop) ensureChildFrame(page *Page, spec jsvm.FrameSpec) (uint64, error) {
	if !l.cfg.Profile.FramesOn() || l.cfg.frameDepth >= maxFrameDepth || l.cfg.frameOpaque {
		return 0, fmt.Errorf("frames disabled or nesting limit reached")
	}
	if l.page != page || page.killed {
		return 0, fmt.Errorf("stale frame document")
	}
	f := page.frames[spec.Node]
	if f != nil && f.spec == spec {
		return f.id, nil
	}
	if f == nil {
		id, err := l.cfg.frameHub.allocate()
		if err != nil {
			return 0, err
		}
		f = &childFrame{id: id}
		page.frames[spec.Node] = f
	} else {
		l.cfg.frameHub.Lock()
		over := l.cfg.frameHub.loads >= maxFrameLoads
		if !over {
			l.cfg.frameHub.loads++
		}
		l.cfg.frameHub.Unlock()
		if over {
			return 0, fmt.Errorf("frame navigation limit reached")
		}
	}
	if f.cancel != nil {
		f.cancel()
	}
	f.spec = spec
	f.revision++
	f.image = nil
	f.generation = 0
	f.pending = true
	revision := f.revision
	ctx, cancel := context.WithCancel(page.ctx)
	f.cancel = cancel
	// Invalidate the previous realm immediately; queued messages cannot cross a
	// src/srcdoc/sandbox change while its replacement is loading.
	l.cfg.frameHub.bind(f.id, nil, "null")
	go func() {
		body, base, opaque, scripts, fetcher, err := l.fetchFrame(ctx, page, spec)
		l.post(func() {
			if ctx.Err() != nil || l.page != page || page.killed || page.frames[spec.Node] != f || f.revision != revision {
				return
			}
			if err != nil {
				f.pending = false
				l.report(fmt.Errorf("iframe: %w", err))
				_, e := page.Sandbox.Dispatch(jsvm.Event{Type: "error", Target: spec.Node, NoBubble: true, NonCancelable: true})
				l.report(e)
				return
			}
			cfg := l.cfg
			cfg.frameID = f.id
			cfg.frameParent = page.frameID
			cfg.frameTop = l.cfg.frameTop
			if cfg.frameTop == 0 {
				cfg.frameTop = page.frameID
			}
			cfg.frameDepth = l.cfg.frameDepth + 1
			cfg.frameOpaque = opaque
			cfg.FrameHeight = spec.Height
			cfg.Width = spec.Width
			cfg.Chunked = false
			cfg.DOMLimits = dom.Limits{Nodes: 2000, Depth: 32, TotalBytes: 256 << 10}
			cfg.RenderLimit = document.Limits{Bytes: 256 << 10, Nodes: 2500, Depth: 40, Rules: 1024, Pixels: 1024 * 768, Height: 16384, LayoutHeight: 16384}
			cfg.Script.MaxTimers = 32
			cfg.Script.MaxListeners = 256
			cfg.Script.MaxMessages = 64
			cfg.Script.MaxMessageBytes = 1 << 20
			cfg.Script.Slice = min(cfg.Script.Slice, 20*time.Millisecond)
			if cfg.Script.Slice <= 0 {
				cfg.Script.Slice = 20 * time.Millisecond
			}
			cfg.Profile.Cookies = profile.CookiesOff
			cfg.Profile.SessionStorage = profile.SessionStorageOff
			cfg.Profile.LocalStorage = profile.LocalStorageOff
			cfg.LocalStorage, cfg.LocalStorageSink = nil, nil // child storage stays disabled
			cfg.Profile.ScriptPrompts = false
			if !scripts {
				cfg.Profile.Scripts = profile.ScriptsOff
			}
			if opaque {
				cfg.Profile.Fetch = profile.FetchOff
			}
			cfg.Subresources.MaxImages = 16
			cfg.Subresources.MaxInFlight = 2
			subFetcher := fetcher.ForFramePage(base, nil, cfg.Profile.Subresources == profile.SubAny)
			cfg.SubresourceTransport = func(string) subres.Transport { return subFetcher }
			child := New(ctx, cfg, frameSink{parent: l, page: page, frame: f, revision: revision})
			f.child = child
			pageFetcher := fetcher.ForFramePage(base, cfg.Profile.Hosts, false)
			go child.Run()
			child.LoadPage(body, base, pageFetcher)
		})
	}()
	return f.id, nil
}
func (l *Loop) fetchFrame(ctx context.Context, page *Page, spec jsvm.FrameSpec) (body, base string, opaque, scripts bool, fetcher *netpolicy.Fetcher, err error) {
	scripts = l.cfg.Profile.ScriptsOn()
	opaque = false
	if spec.HasSandbox {
		opaque = true
		scripts = false
		for _, token := range strings.Fields(spec.Sandbox) {
			switch token {
			case "allow-scripts":
				scripts = l.cfg.Profile.ScriptsOn()
			case "allow-same-origin":
				opaque = false
			case "allow-forms", "allow-popups", "allow-modals":
				// Grants for capabilities child frames do not have (form
				// submission, new windows, dialogs): accepting them changes
				// nothing, refusing them would drop pages like GitHub's login.
			default:
				err = fmt.Errorf("unsupported iframe sandbox token %q", token)
				return
			}
		}
	}
	fetcher = page.Fetcher.ForFrameSources(page.Base, l.cfg.Profile.FrameOrigins(page.Base), l.cfg.Profile.Frames == profile.FramesAny)
	base = page.Base
	if spec.HasSrcdoc {
		if len(spec.Srcdoc) > dom.MaxFragmentBytes {
			err = fmt.Errorf("iframe srcdoc too large")
			return
		}
		body = spec.Srcdoc
		return
	}
	if spec.Src == "" || spec.Src == "about:blank" {
		body = "<html><body></body></html>"
		return
	}
	u, e := url.Parse(spec.Src)
	if e != nil {
		err = e
		return
	}
	parent, e := url.Parse(page.Base)
	if e != nil {
		err = e
		return
	}
	u = parent.ResolveReference(u)
	response, e := fetcher.Do(ctx, netpolicy.Request{URL: u.String(), Kind: netpolicy.KindFrame, OmitCookies: true})
	if e != nil {
		err = e
		return
	}
	base = response.URL
	body = response.Body
	mt, _, e := mime.ParseMediaType(response.Headers["content-type"])
	if e != nil || mt != "text/html" {
		err = fmt.Errorf("iframe requires text/html")
		return
	}
	// Fail closed on embedding restrictions not implemented by this mode.
	xfo := strings.ToUpper(strings.TrimSpace(response.Headers["x-frame-options"]))
	targetOrigin, _ := origin.Of(base)
	parentOrigin, _ := origin.Of(page.Base)
	if xfo != "" && (xfo != "SAMEORIGIN" || targetOrigin != parentOrigin || l.cfg.frameDepth > 0) {
		err = fmt.Errorf("iframe denied by X-Frame-Options")
		return
	}
	if csp := response.Headers["content-security-policy"]; csp != "" {
		for _, directive := range strings.Split(csp, ";") {
			fields := strings.Fields(directive)
			if strings.Contains(directive, ",") || len(fields) > 0 && strings.EqualFold(fields[0], "sandbox") {
				err = fmt.Errorf("unsupported iframe CSP policy")
				return
			}
			if len(fields) > 0 && strings.EqualFold(fields[0], "frame-ancestors") {
				allowed := false
				for _, v := range fields[1:] {
					if v == "'self'" && parentOrigin == targetOrigin || v == parentOrigin {
						allowed = true
					}
				}
				if !allowed || l.cfg.frameDepth > 0 {
					err = fmt.Errorf("iframe denied by frame-ancestors")
					return
				}
			}
		}
	}
	return
}
func (l *Loop) reconcileFrames() {
	p := l.page
	if p == nil || p.killed || p.frames == nil {
		return
	}
	seen := map[dom.NodeID]bool{}
	var ids []uint64
	dom.Walk(p.Tree.Root(), func(n *dom.Node) {
		if n.Tag() != "iframe" {
			return
		}
		seen[n.ID()] = true
		spec := p.Sandbox.FrameSpec(n.ID())
		id, err := l.ensureChildFrame(p, spec)
		if err == nil {
			ids = append(ids, id)
		}
	})
	for node, f := range p.frames {
		if !seen[node] {
			if f.cancel != nil {
				f.cancel()
			}
			l.cfg.frameHub.remove(f.id)
			delete(p.frames, node)
			if p.frameFocus == node {
				p.frameFocus = 0
				l.announceFrameFocus()
			}
			p.Tree.MarkDirty()
		}
	}
	p.Sandbox.SetFrameChildren(ids)
}
func (l *Loop) closeFrames(p *Page) {
	for _, f := range p.frames {
		if f.cancel != nil {
			f.cancel()
		}
		l.cfg.frameHub.remove(f.id)
	}
	if p.frameID != 0 && l.cfg.frameHub != nil {
		h := l.cfg.frameHub
		h.Lock()
		if h.endpoints[p.frameID].loop == l {
			delete(h.endpoints, p.frameID)
		}
		h.Unlock()
	}
	p.frames = nil
}

type frameSink struct {
	parent   *Loop
	page     *Page
	frame    *childFrame
	revision uint64
}

func (s frameSink) Send(msg tea.Msg) {
	s.parent.tryPost(func() {
		if s.parent.page != s.page || s.page.killed || s.frame.revision != s.revision || s.page.frames[s.frame.spec.Node] != s.frame {
			return
		}
		switch m := msg.(type) {
		case FrameMsg:
			if m.Err != nil {
				s.frame.pending = false
			}
			if m.FrameImage != nil && m.Err == nil {
				s.frame.image = m.FrameImage
				s.frame.offset = m.FrameOrigin
				s.frame.generation = m.Gen
				s.page.Tree.MarkDirty()
			}
		case documentLoadedMsg:
			s.frame.pending = false
			_, err := s.page.Sandbox.Dispatch(jsvm.Event{Type: "load", Target: s.frame.spec.Node, NoBubble: true, NonCancelable: true})
			s.parent.report(err)
		case LogMsg:
			if strings.HasPrefix(m.Line, "parse:") {
				s.frame.pending = false
			}
			s.parent.send(LogMsg{m.Level, "iframe: " + m.Line})
		case ScriptNavigateMsg:
			// Child navigation never replaces its parent. It remains inside both the
			// original frame grant and the child's script-navigation policy.
			if s.frame.child == nil {
				return
			}
			p := s.frame.child.cfg.Profile
			sourceOrigin, _ := origin.Of(m.Source)
			targetOrigin, _ := origin.Of(m.Href)
			if p.Navigate != profile.NavFree || p.ScriptNavigation != profile.ScriptNavOrigin || sourceOrigin != targetOrigin {
				s.parent.report(fmt.Errorf("iframe script navigation denied"))
				return
			}
			spec := s.frame.spec
			spec.HasSrcdoc = false
			spec.Srcdoc = ""
			spec.Src = m.Href
			n := s.page.Tree.Node(spec.Node)
			if n == nil {
				return
			}
			if err := s.page.Tree.SetAttr(n, "src", spec.Src); err != nil {
				s.parent.report(err)
				return
			}
			s.page.Tree.RemoveAttr(n, "srcdoc")
			_, err := s.parent.ensureChildFrame(s.page, spec)
			s.parent.report(err)
		case NavigateMsg:
			s.parent.report(fmt.Errorf("iframe link/form navigation is not supported yet"))
		}
	})
}

type documentLoadedMsg struct{}

func (l *Loop) frameImages(p *Page, opts *document.Options) (map[dom.NodeID]dom.FrameImage, map[dom.NodeID]framePresentation) {
	html := map[dom.NodeID]dom.FrameImage{}
	shown := map[dom.NodeID]framePresentation{}
	for node, f := range p.frames {
		if f.image == nil {
			continue
		}
		key := fmt.Sprintf("ntbrowser-frame-%d", f.id)
		if opts.Images == nil {
			opts.Images = map[string]image.Image{}
		}
		opts.Images[key] = f.image
		html[node] = dom.FrameImage{Source: key, Width: f.spec.Width, Height: f.spec.Height}
		shown[node] = framePresentation{f.offset, f.revision, f, f.image, f.generation}
	}
	return html, shown
}
func (l *Loop) clickFrame(area image.Rectangle) bool {
	p := l.page
	for _, b := range p.boxes {
		if b.Anchor == "" || !b.Bounds.Overlaps(area) {
			continue
		}
		n := p.Tree.ByID(b.Anchor)
		if n == nil || n.Tag() != "iframe" {
			continue
		}
		shown, ok := p.displayedFrames[n.ID()]
		if !ok || shown.frame.child == nil || shown.frame.revision != shown.revision || shown.frame.generation != shown.generation {
			return true
		}
		width, height := max(1, b.Bounds.Dx()), max(1, b.Bounds.Dy())
		bounds := shown.image.Bounds()
		x := (area.Min.X - b.Bounds.Min.X) * bounds.Dx() / width
		y := (area.Min.Y - b.Bounds.Min.Y) * bounds.Dy() / height
		p.frameFocus = n.ID()
		l.announceFrameFocus()
		shown.frame.child.ClickAtFrame(shown.generation, image.Rect(x, y, x+1, y+1).Add(shown.offset))
		return true
	}
	return false
}
