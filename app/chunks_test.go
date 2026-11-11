// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
)

func newChunkApp(t *testing.T, html string) (*Model, chan tea.Msg) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chan tea.Msg, 256)
	l := loop.New(ctx, loop.Config{Chunked: true, Profile: testLoopProfile, Width: 400, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, chanSink(sink))
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), Options{})
	m.current = mustURL(t, "https://example.test/page")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 15})
	l.LoadPage(html, m.current.String(), nil)
	return m, sink
}

func chunkAppFrame(t *testing.T, m *Model, sink chan tea.Msg) loop.FrameMsg {
	t.Helper()
	timer := time.NewTimer(patience)
	defer timer.Stop()
	for {
		select {
		case msg := <-sink:
			_, cmd := m.Update(msg)
			drive(m, cmd)
			if f, ok := msg.(loop.FrameMsg); ok {
				return f
			}
		case <-timer.C:
			t.Fatal("no frame")
			return loop.FrameMsg{}
		}
	}
}

func TestChunkAppNavigationSelectionAndFragments(t *testing.T) {
	var html strings.Builder
	html.WriteString("<body>")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&html, `<section id="m%d"><p>Message %d</p><a id="a%d" href="/next">open</a></section>`, i, i, i)
	}
	html.WriteString("</body>")
	m, sink := newChunkApp(t, html.String())
	first := chunkAppFrame(t, m, sink)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	if m.chunk.Index != 0 || m.chunk.Count != 200 {
		t.Fatalf("first chunk: %+v", m.chunk)
	}
	drive(m, m.viewer.SelectRange(0, 7))
	if m.viewer.SelectedText() == "" {
		t.Fatal("vacuous selection")
	}
	if !m.chunkKey("]") {
		t.Fatal("next chunk key ignored")
	}
	second := chunkAppFrame(t, m, sink)
	if second.Err != nil || m.chunk.Index != 1 {
		t.Fatalf("second: %+v %v", m.chunk, second.Err)
	}
	if m.viewer.SelectedText() != "" {
		t.Fatal("selection leaked across chunks")
	}
	if !m.chunkKey("ctrl+end") {
		t.Fatal("last chunk key ignored")
	}
	last := chunkAppFrame(t, m, sink)
	if last.Err != nil || m.chunk.Index != 199 {
		t.Fatalf("last: %+v %v", m.chunk, last.Err)
	}
	seq := m.navSeq
	if !m.chunkFragment("https://example.test/page#m20") {
		t.Fatal("local fragment not recognized")
	}
	fragment := chunkAppFrame(t, m, sink)
	if fragment.Err != nil || m.chunk.Index != 20 || m.navSeq != seq {
		t.Fatalf("fragment restarted page or missed chunk: %+v %v", m.chunk, fragment.Err)
	}
	if m.chunkFragment("https://other.test/page#m20") {
		t.Fatal("external URL bypassed navigation gate")
	}
	drive(m, m.viewer.SetLayoutMode(nthtml.LayoutTTY))
	if !m.chunkKey("]") {
		t.Fatal("TTY chunk navigation unavailable")
	}
	tty := chunkAppFrame(t, m, sink)
	if tty.Err != nil || m.chunk.Index != 21 {
		t.Fatalf("TTY next: %+v %v", m.chunk, tty.Err)
	}
	if !strings.Contains(m.View().Content, "chunk 22/200") {
		t.Fatal("chunk position missing from help")
	}
}

func TestChunkAppSkipsOversizedUnit(t *testing.T) {
	html := `<body><p>first</p><pre>` + strings.Repeat("x", 17000) + `</pre><p>last</p></body>`
	m, sink := newChunkApp(t, html)
	if f := chunkAppFrame(t, m, sink); f.Err != nil {
		t.Fatal(f.Err)
	}
	m.chunkKey("]")
	if f := chunkAppFrame(t, m, sink); f.Err == nil || !m.chunkFailed || m.chunk.Index != 1 {
		t.Fatalf("oversized chunk: %+v %v", m.chunk, f.Err)
	}
	if m.chunkMouseReady() {
		t.Fatal("failed chunk accepts hits against old image")
	}
	m.chunkKey("]")
	f := chunkAppFrame(t, m, sink)
	if f.Err != nil || m.chunkFailed || m.chunk.Index != 2 {
		t.Fatalf("recovery: %+v %v", m.chunk, f.Err)
	}
	text, _ := m.viewer.Result().TTY(context.Background(), 80)
	if !strings.Contains(text, "last") {
		t.Fatal("last chunk not readable")
	}
	if strings.HasPrefix(m.status.text, "render:") {
		t.Fatalf("stale render error: %s", m.status.text)
	}
}

func TestChunkMouseFragmentRespectsPreventDefault(t *testing.T) {
	html := `<body><a id="link" href="#target">jump</a><pre>` + strings.Repeat("line\n", 60) + `</pre><p id="target">target</p><script>document.getElementById("link").addEventListener("click",e=>{e.preventDefault();console.log("prevented");});</script></body>`
	m, sink := newChunkApp(t, html)
	f := chunkAppFrame(t, m, sink)
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	drive(m, m.viewer.Picture().SetTerminal(true, 10, 20))
	if !m.chunkMouseReady() {
		t.Fatal("picture not presented")
	}
	before, ok := m.viewer.DocumentViewport()
	if !ok {
		t.Fatal("no viewport")
	}
	var x, y int
	found := false
	for yy := 0; yy < m.viewRows() && !found; yy++ {
		for xx := 0; xx < m.cols; xx++ {
			if _, ok := m.viewer.LinkAtCell(image.Pt(xx, yy)); ok {
				x, y, found = xx, yy, true
				break
			}
		}
	}
	if !found {
		t.Fatal("no link cell")
	}
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	drive(m, cmd)
	_, cmd = m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	drive(m, cmd)
	timer := time.NewTimer(patience)
	defer timer.Stop()
	for {
		select {
		case msg := <-sink:
			if v, ok := msg.(loop.NavigateMsg); ok {
				t.Fatalf("prevented fragment navigated: %s", v.Href)
			}
			if v, ok := msg.(loop.LogMsg); ok && v.Line == "prevented" {
				after, _ := m.viewer.DocumentViewport()
				if after != before {
					t.Fatalf("viewer scrolled before preventDefault: %v -> %v", before, after)
				}
				if !m.chunkMouseReady() {
					t.Fatal("discarded graphics command stranded the picture")
				}
				return
			}
		case <-timer.C:
			t.Fatal("click never reached listener")
			return
		}
	}
}
