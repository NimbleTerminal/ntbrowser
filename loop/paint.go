// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"sync"
	"time"

	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
)

const paintDeadline = 2 * time.Second

// paintQueue holds one latest request. It never grows with scroll input.
type paintQueue struct {
	sync.Mutex
	request  *nthtml.PaintRequest
	viewport nthtml.PaintViewport
}

type paintWorker struct {
	painter  *document.Painter
	result   *document.Result
	revision uint64
	ctx      context.Context
}

func (w *paintWorker) close() {
	if w.painter != nil {
		w.painter.Close()
	}
	w.painter, w.result, w.ctx = nil, nil, nil
	w.revision = 0
}

// One worker owns all layout and paint calls, including Painter.Close.
func (l *Loop) runRenderer() {
	w := new(paintWorker)
	defer w.close()
	for {
		if l.ctx.Err() != nil {
			return
		}
		// Prefer layout over obsolete scrolling work.
		select {
		case job := <-l.renderJobs:
			job(w)
			continue
		default:
		}
		var pageDone <-chan struct{}
		if w.ctx != nil {
			pageDone = w.ctx.Done()
		}
		select {
		case <-l.ctx.Done():
			return
		case <-pageDone:
			w.close()
		case job := <-l.renderJobs:
			job(w)
		case <-l.paintWake:
			l.paintQueue.Lock()
			req := l.paintQueue.request
			l.paintQueue.request = nil
			l.paintQueue.Unlock()
			if req == nil || req.Context == nil || req.Context.Err() != nil {
				continue
			}
			ready := nthtml.PaintReady{Request: *req}
			if w.painter == nil || w.result != req.Result || w.revision != req.Revision || w.ctx.Err() != nil {
				ready.Err = context.Canceled
			} else {
				ctx, cancel := context.WithTimeout(req.Context, paintDeadline)
				stop := context.AfterFunc(w.ctx, cancel)
				ready.Image, ready.Err = w.painter.Paint(ctx, req.Plan.Area, req.Plan.Scale)
				stop()
				cancel()
			}
			l.send(ready)
		}
	}
}

// Paint hands a viewer request to the serial worker without blocking the UI.
func (l *Loop) Paint(req nthtml.PaintRequest) {
	if req.Context == nil || req.Context.Err() != nil || l.ctx.Err() != nil {
		return
	}
	l.paintQueue.Lock()
	l.paintQueue.request = &req
	l.paintQueue.Unlock()
	select {
	case l.paintWake <- struct{}{}:
	default:
	}
}

// SetViewport stores the latest UI snapshot without queueing a VM task.
func (l *Loop) SetViewport(v nthtml.PaintViewport) {
	l.paintQueue.Lock()
	l.paintQueue.viewport = v
	l.paintQueue.Unlock()
}
func (l *Loop) viewport() nthtml.PaintViewport {
	l.paintQueue.Lock()
	defer l.paintQueue.Unlock()
	return l.paintQueue.viewport
}

// FrameCurrent is an input guard, not permission to read VM-owned state.
func (l *Loop) FrameCurrent(gen uint64) bool {
	return gen != 0 && gen == l.currentFrame.Load() && l.frameNavigation.Load() == l.navGen.Load()
}
func (l *Loop) NavigationCurrent(gen uint64) bool { return gen == l.navGen.Load() }
