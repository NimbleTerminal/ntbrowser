// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"image"
	"math"
	"nimbleterminal.dev/ntbrowser/jsvm"
)

// Called under frameOwner, shared by every realm in this frame tree.
// The hub mutex protects the endpoint registry from message forwarders.
func (l *Loop) frameDOM(p *Page, sourceRevision uint64, r jsvm.DOMRequest) (jsvm.DOMReply, error) {
	denied := func() (jsvm.DOMReply, error) {
		return jsvm.DOMReply{}, &jsvm.DOMError{Name: "SecurityError", Message: "shared frame DOM access denied or stale"}
	}
	if l.cfg.Profile.FrameDOM != "same-origin" || l.page != p || p.killed || l.cfg.frameHub == nil {
		return denied()
	}
	h := l.cfg.frameHub
	h.Lock()
	src, ok := h.endpoints[p.frameID]
	dst, found := h.endpoints[r.Ref.Frame]
	h.Unlock()
	if !ok || !found || !frameDOMCurrent(h, src.loop) || !frameDOMCurrent(h, dst.loop) || src.revision != sourceRevision || src.origin == "null" || src.origin != dst.origin || dst.loop == nil {
		return denied()
	}
	target := dst.loop
	if target.cfg.frameOwner != l.cfg.frameOwner || target.cfg.Profile.FrameDOM != "same-origin" || target.page == nil || target.page.killed || target.ctx.Err() != nil {
		return denied()
	}
	if r.Ref.Revision != 0 && r.Ref.Revision != dst.revision {
		return denied()
	}
	r.Ref.Revision = dst.revision
	if r.Op == "scrollPosition" || r.Op == "scrollWindow" {
		if target.cfg.FrameHeight <= 0 {
			return jsvm.DOMReply{}, &jsvm.DOMError{Name: "NotSupportedError", Message: "top-level window scrolling is not exposed"}
		}
		if r.Op == "scrollWindow" {
			coordinate := func(n float64) int {
				if math.IsNaN(n) || math.IsInf(n, 0) {
					return 0
				}
				return int(math.Max(-1e7, math.Min(1e7, n)))
			}
			point := image.Pt(coordinate(r.X), coordinate(r.Y))
			if r.Relative {
				point = point.Add(target.page.frameScroll)
			}
			target.setFrameScroll(point)
		}
		return jsvm.DOMReply{X: float64(target.page.frameScroll.X), Y: float64(target.page.frameScroll.Y)}, nil
	}
	if r.Op == "retain" {
		if p.domPins == nil {
			p.domPins = map[jsvm.DOMRef]bool{}
		}
		if p.domPins[r.Ref] {
			return jsvm.DOMReply{}, nil
		}
		if len(p.domPins) >= jsvm.MaxForeignNodes {
			return jsvm.DOMReply{}, &jsvm.DOMError{Name: "QuotaExceededError", Message: "shared DOM handle limit reached"}
		}
	}
	result, err := target.page.Sandbox.ApplySharedDOM(r)
	if err == nil && r.Op == "retain" {
		p.domPins[r.Ref] = true
	}
	if target.page.Tree.Dirty() {
		target.currentFrame.Store(0)
		target.scheduleFrame() // No target JS reentry; its ordinary next turn reconciles resources.
	}
	return result, err
}
func (l *Loop) releaseFrameDOM(p *Page) {
	h := l.cfg.frameHub
	if h == nil {
		return
	}
	for ref := range p.domPins {
		h.Lock()
		dst, ok := h.endpoints[ref.Frame]
		h.Unlock()
		if ok && dst.revision == ref.Revision && dst.loop != nil && dst.loop.page != nil && !dst.loop.page.killed {
			dst.loop.page.Sandbox.ReleaseSharedDOM(ref.Node)
		}
	}
	p.domPins = nil
}

// Attribute edits and detachment take effect synchronously, even before the
// owner's next reconciliation turn installs/removes the child endpoint.
func frameDOMCurrent(h *frameHub, l *Loop) bool {
	if l == nil || l.page == nil || l.page.killed {
		return false
	}
	if l.cfg.frameParent == 0 {
		return true
	}
	h.Lock()
	parent, ok := h.endpoints[l.cfg.frameParent]
	h.Unlock()
	if !ok || parent.loop == nil || !frameDOMCurrent(h, parent.loop) {
		return false
	}
	for _, f := range parent.loop.page.frames {
		if f.id != l.cfg.frameID {
			continue
		}
		n := parent.loop.page.Tree.Node(f.spec.Node)
		return f.child == l && n != nil && parent.loop.page.Tree.Attached(n) && parent.loop.page.Sandbox.FrameSpec(n.ID()) == f.spec
	}
	return false
}
