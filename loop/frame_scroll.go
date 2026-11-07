// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"image"
	"nimbleterminal.dev/ntbrowser/jsvm"
)

func (l *Loop) installFrameScroll(p *Page, cfg *jsvm.Config) {
	if l.cfg.FrameHeight <= 0 {
		return
	}
	cfg.ScrollPosition = func() (float64, float64) { return float64(p.frameScroll.X), float64(p.frameScroll.Y) }
	cfg.Scroll = func(x, y float64, relative bool) {
		if l.page != p || p.killed {
			return
		}
		point := image.Pt(int(x), int(y))
		if relative {
			point = point.Add(p.frameScroll)
		}
		l.setFrameScroll(point)
	}
}
func (l *Loop) setFrameScroll(point image.Point) {
	p := l.page
	if p == nil || p.killed || l.cfg.FrameHeight <= 0 {
		return
	}
	maxX, maxY := 0, 16384-l.cfg.FrameHeight
	if p.result != nil {
		maxX = max(0, p.result.Bounds().Dx()-l.cfg.Width)
		maxY = max(0, p.result.Bounds().Dy()-l.cfg.FrameHeight)
	}
	point.X = max(0, min(maxX, point.X))
	point.Y = max(0, min(maxY, point.Y))
	if point == p.frameScroll {
		return
	}
	p.frameScroll = point
	p.scrollEvent = true
	p.Tree.MarkDirty()
	l.currentFrame.Store(0)
	l.scheduleFrame()
}

// ScrollFrameAt routes a wheel gesture into the deepest displayed child.
// A gesture over a frame is contained there, including at its scroll boundary.
func (l *Loop) ScrollFrameAt(gen uint64, area image.Rectangle, delta image.Point) {
	l.tryPost(func() {
		if l.page == nil || !l.FrameCurrent(gen) || l.page.Tree.Dirty() {
			return
		}
		l.routeFrameScroll(area, delta)
	})
}
func (l *Loop) routeFrameScroll(area image.Rectangle, delta image.Point) bool {
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
		child := shown.frame.child
		if child.page == nil || !child.FrameCurrent(shown.generation) || child.page.Tree.Dirty() {
			return true
		}
		point := image.Pt((area.Min.X-b.Bounds.Min.X)*shown.image.Bounds().Dx()/max(1, b.Bounds.Dx()), (area.Min.Y-b.Bounds.Min.Y)*shown.image.Bounds().Dy()/max(1, b.Bounds.Dy())).Add(shown.offset)
		local := image.Rectangle{Min: point, Max: point.Add(image.Pt(1, 1))}
		if !child.routeFrameScroll(local, delta) {
			child.setFrameScroll(child.page.frameScroll.Add(delta))
		}
		p.frameFocus = n.ID()
		l.announceFrameFocus()
		return true
	}
	return false
}
func (l *Loop) frameRegions(p *Page) []image.Rectangle {
	var regions []image.Rectangle
	for _, b := range p.boxes {
		n := p.Tree.ByID(b.Anchor)
		if n != nil && n.Tag() == "iframe" {
			if _, ok := p.displayedFrames[n.ID()]; ok {
				regions = append(regions, b.Bounds)
			}
		}
	}
	return regions
}

// FrameFocusMsg lets the UI keep navigation keys inside a clicked frame while
// retaining its own quit, address, and search controls.
type FrameFocusMsg struct {
	Gen    uint64
	Active bool
}

func (l *Loop) announceFrameFocus() {
	if l.cfg.frameDepth == 0 && l.page != nil {
		l.send(FrameFocusMsg{Gen: l.page.frameGen, Active: l.page.frameFocus != 0})
	}
}
func (l *Loop) LeaveFrameFocus(gen uint64) {
	l.tryPost(func() {
		if l.page != nil && l.page.frameGen == gen {
			l.page.frameFocus = 0
			l.announceFrameFocus()
		}
	})
}
func (l *Loop) ScrollFocusedFrameAt(gen uint64, key string) {
	l.tryPost(func() {
		if l.page == nil || !l.FrameCurrent(gen) || l.page.Tree.Dirty() {
			return
		}
		current := l
		for current.page != nil && current.page.frameFocus != 0 {
			shown, ok := current.page.displayedFrames[current.page.frameFocus]
			if !ok || shown.frame.child == nil || shown.revision != shown.frame.revision || shown.generation != shown.frame.generation {
				return
			}
			child := shown.frame.child
			if child.page == nil || child.page.Tree.Dirty() || !child.FrameCurrent(shown.generation) {
				return
			}
			current = child
		}
		if current == l || current.page == nil {
			return
		}
		target := current
		targetGen := target.page.frameGen
		target.tryPost(func() {
			if target.page == nil || !target.FrameCurrent(targetGen) || target.page.Tree.Dirty() {
				return
			}
			if target.cfg.Profile.ScriptsOn() {
				name := domKeys[key]
				if name == "" {
					name = key
				}
				_, err := target.page.Sandbox.Dispatch(jsvm.Event{Type: "keydown", Key: name})
				target.report(err)
			}
			point := target.page.frameScroll
			switch key {
			case "up":
				point.Y -= 40
			case "down":
				point.Y += 40
			case "left":
				point.X -= 40
			case "right":
				point.X += 40
			case "pgup":
				point.Y -= max(1, target.cfg.FrameHeight-20)
			case "pgdown":
				point.Y += max(1, target.cfg.FrameHeight-20)
			case "home":
				point.Y = 0
			case "end":
				point.Y = 16384
			default:
				return
			}
			target.setFrameScroll(point)
		})
	})
}
