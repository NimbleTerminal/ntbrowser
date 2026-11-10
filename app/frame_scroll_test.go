// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"html"
	"image"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/nthtml"
	"testing"
	"time"
)

func TestWheelAndKeyboardStayInsideFrame(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testLoopProfile
	p.Frames = profile.FramesOrigin
	sink := make(chanSink, 256)
	l := loop.New(ctx, loop.Config{Profile: p, Width: 800, MinFrame: time.Millisecond}, sink)
	go l.Run()
	m := New(l, p, Options{Start: "https://example.test/"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	child := `<div style="height:1500px;background:red">long child</div><script>addEventListener('scroll',()=>parent.postMessage('scroll:'+scrollY,'*'));addEventListener('keydown',e=>parent.postMessage('key:'+e.key,'*'))</script>`
	l.LoadPage(`<iframe width=300 height=150 srcdoc="`+html.EscapeString(child)+`"></iframe><div style="height:3000px">long parent</div><script>addEventListener('message',e=>console.log(e.data))</script>`, "https://example.test/", nil)
	logs := map[string]bool{}
	pump := func(want string, old uint64) {
		t.Helper()
		seen := want == ""
		deadline := time.After(5 * time.Second)
		for {
			select {
			case <-deadline:
				t.Fatalf("waiting for %q, status %q, regions %v, focus %v", want, m.status.text, m.frameRegions, m.embeddedFocus)
			case msg := <-sink:
				if log, ok := msg.(loop.LogMsg); ok {
					if log.Level == "error" {
						t.Fatal(log.Line)
					}
					logs[log.Line] = true
					if log.Line == want {
						seen = true
					}
				}
				_, cmd := m.Update(msg)
				switch msg.(type) {
				case loop.FrameMsg, nthtml.PaintReady:
					drive(m, cmd)
				}
				if seen && m.gen > old && len(m.frameRegions) > 0 && m.frameInputReady() {
					return
				}
			}
		}
	}
	pump("", 0)
	cell := image.Pt(-1, -1)
	for y := 0; y < m.viewRows() && cell.X < 0; y++ {
		for x := 0; x < m.cols; x++ {
			area, ok := m.viewer.CellToDocument(image.Pt(x, y))
			if ok && area.Min.In(m.frameRegions[0].Inset(5)) {
				cell = image.Pt(x, y)
				break
			}
		}
	}
	if cell.X < 0 {
		t.Fatal("no visible child cell")
	}
	before := m.viewer.PaintViewport().Origin
	gen := m.gen
	m.Update(tea.MouseWheelMsg{X: cell.X, Y: cell.Y, Button: tea.MouseWheelDown})
	pump("scroll:40", gen)
	if m.viewer.PaintViewport().Origin != before {
		t.Fatal("wheel scrolled parent")
	}
	if !m.embeddedFocus {
		t.Fatal("wheel did not focus child")
	}
	gen = m.gen
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	pump("scroll:170", gen)
	if !logs["key:PageDown"] {
		t.Fatal("child missed navigation keydown")
	}
	if m.viewer.PaintViewport().Origin != before {
		t.Fatal("PageDown scrolled parent")
	}
	// While a replacement presentation is pending, scroll keys must not
	// fall through and move the top-level viewer.
	m.resetView = true
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.viewer.PaintViewport().Origin != before {
		t.Fatal("pending frame let keyboard scroll parent")
	}
	m.resetView = false
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.embeddedFocus {
		t.Fatal("Escape kept frame focus")
	}
}
