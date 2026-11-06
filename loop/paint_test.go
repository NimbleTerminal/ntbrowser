// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"bytes"
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
)

func awaitPaint(t *testing.T, sink chanSink) nthtml.PaintReady {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case msg := <-sink:
			if ready, ok := msg.(nthtml.PaintReady); ok {
				return ready
			}
		case <-timeout.C:
			t.Fatal("paint timed out")
			return nthtml.PaintReady{}
		}
	}
}

func TestViewportWorkerAtomicBandsAndReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 128)
	l := New(ctx, Config{Profile: testProfile, Width: 400, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, sink)
	go l.Run()
	viewport := nthtml.PaintViewport{Cols: 40, Rows: 12, CellWidth: 10, CellHeight: 20}
	l.SetViewport(viewport)
	l.LoadPage(`<body style="background:#def">`+strings.Repeat(`<div style="height:1000px;background:#fa3">text</div>`, 40), "https://example.test/", nil)
	f := nextFrame(t, sink)
	if f.Err != nil || f.Painted == nil || f.Result.Image != nil {
		t.Fatalf("non-atomic CSS frame: %v", f.Err)
	}
	if f.Painted.Revision != f.Gen || f.Painted.Result != f.Result || f.Painted.Plan.Area.Dy() >= f.Result.Bounds().Dy() {
		t.Fatal("invalid/unbounded frame")
	}
	viewport.Origin.Y = 17000
	plan, err := nthtml.PlanPaint(f.Result, viewport)
	if err != nil {
		t.Fatal(err)
	}
	req := nthtml.PaintRequest{Result: f.Result, Revision: f.Gen, Sequence: 1, Plan: plan, Context: ctx}
	l.Paint(req)
	ready := awaitPaint(t, sink)
	if ready.Err != nil {
		t.Fatal(ready.Err)
	}
	want, err := f.Result.Paint(ctx, plan.Area, plan.Scale)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ready.Image.Pix, want.Pix) {
		t.Fatal("worker paint changed CSS pixels")
	}
	// A serial worker barrier makes coalescing deterministic.
	entered, release := make(chan struct{}), make(chan struct{})
	l.renderJobs <- func(w *paintWorker) { close(entered); <-release }
	<-entered
	req.Sequence = 2
	l.Paint(req)
	req.Sequence = 3
	l.Paint(req)
	close(release)
	if got := awaitPaint(t, sink); got.Request.Sequence != 3 || got.Err != nil {
		t.Fatalf("did not coalesce: %+v", got)
	}
	oldPainter := make(chan *document.Painter, 1)
	l.renderJobs <- func(w *paintWorker) { oldPainter <- w.painter }
	old := <-oldPainter
	l.LoadPage("<p>replacement</p>", "https://example.test/new", nil)
	if l.FrameCurrent(f.Gen) {
		t.Fatal("navigation retained old input revision")
	}
	fresh := nextFrame(t, sink)
	if fresh.Err != nil {
		t.Fatal(fresh.Err)
	}
	if _, err := old.Paint(ctx, image.Rect(0, 0, 1, 1), 1); err == nil {
		t.Fatal("old painter not closed")
	}
	l.Paint(req)
	if got := awaitPaint(t, sink); !errors.Is(got.Err, context.Canceled) {
		t.Fatalf("old revision painted: %v", got.Err)
	}
}

func TestViewportInputInvalidatedBeforeReflow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 128)
	l := New(ctx, Config{Profile: testProfile, Width: 400, Fonts: document.MinimalFonts, MinFrame: time.Hour}, sink)
	go l.Run()
	l.LoadPage(`<a id="a" href="/old">old</a><p id="p">before</p>`, "https://example.test/", nil)
	f := nextFrame(t, sink)
	if !l.FrameCurrent(f.Gen) {
		t.Fatal("fresh frame not current")
	}
	mutated := make(chan struct{})
	l.post(func() {
		if err := l.page.Tree.SetText(l.page.Tree.ByID("p"), "after"); err != nil {
			panic(err)
		}
	})
	l.post(func() { close(mutated) })
	<-mutated
	if l.FrameCurrent(f.Gen) {
		t.Fatal("dirty DOM still accepts hits")
	}
	l.LinkActivatedAt(f.Gen, f.Result.Links[0])
	l.post(func() { l.send(LogMsg{Level: "test", Line: "input barrier"}) })
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		select {
		case msg := <-sink:
			if _, ok := msg.(NavigateMsg); ok {
				t.Fatal("old hit navigated")
			}
			if log, ok := msg.(LogMsg); ok && log.Line == "input barrier" {
				return
			}
		case <-timeout.C:
			t.Fatal("input barrier timed out")
		}
	}
}

func BenchmarkCJKViewportPaint(b *testing.B) {
	if len(fallbackFaces) == 0 {
		b.Skip("nocjk build")
	}
	ctx := context.Background()
	for _, n := range []int{100, 1100} {
		name := "short"
		if n > 100 {
			name = "near_limit"
		}
		b.Run(name, func(b *testing.B) {
			html := strings.Repeat(`<p>Latin <b>你好漢字</b> and <i>中文</i> text.</p>`, n)
			r, err := document.Render(ctx, strings.NewReader(html), document.Options{Width: 800, NoImage: true, FallbackFaces: fallbackFaces})
			if err != nil {
				b.Fatal(err)
			}
			if r.MissingGlyphs != 0 {
				b.Fatalf("missing CJK glyphs: %d", r.MissingGlyphs)
			}
			area := image.Rect(0, r.Bounds().Dy()/2, 800, r.Bounds().Dy()/2+400)
			b.Run("uncached", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := r.Paint(ctx, area, 1); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("cold", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					p, err := r.NewPainter(ctx)
					if err != nil {
						b.Fatal(err)
					}
					_, err = p.Paint(ctx, area, 1)
					p.Close()
					if err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("warm", func(b *testing.B) {
				p, err := r.NewPainter(ctx)
				if err != nil {
					b.Fatal(err)
				}
				defer p.Close()
				b.ReportAllocs()
				for b.Loop() {
					if _, err := p.Paint(ctx, area, 1); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

func TestViewportResizeCoalescesOutsideInbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	l := New(ctx, Config{Width: 400, Inbox: 1, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, sink)
	// Fill the VM inbox before Run. Resizes must retain the last width.
	l.inbox <- func() {}
	l.Resize(600)
	l.Resize(900)
	go l.Run()
	l.LoadPage("<p>resized</p>", "https://example.test/", nil)
	f := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return f.Err == nil && f.Result.Bounds().Dx() == 900 })
	if f.Painted == nil {
		t.Fatal("resize lost atomic band")
	}
}
