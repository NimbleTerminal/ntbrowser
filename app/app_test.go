// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"net/url"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/loop"
)

type dropSink struct{}

func (dropSink) Send(tea.Msg) {}

func renderTall(t *testing.T, body string) *document.Result {
	t.Helper()
	res, err := document.Render(context.Background(), strings.NewReader(
		`<!doctype html><html><body>`+body+`<div style="height:3000px"></div></body></html>`), document.Options{Width: 400, NoImage: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A re-flow of the same page keeps the reader's scroll offset; the first
// frame of a new page starts at the top. Both go through the same FrameMsg
// path, distinguished only by pageLoadedMsg having arrived in between.
func TestReflowKeepsScrollAndNewPageResetsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400}, dropSink{})
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})

	first := renderTall(t, `<p>v1</p>`)
	m.Update(cssFrame(t, m, loop.FrameMsg{Gen: 1, Result: first}))
	if m.viewer == nil {
		t.Fatal("no viewer after first frame")
	}
	_ = m.viewer.Pan(0, 900)
	if y := m.viewer.PaintViewport().Origin.Y; y != 900 {
		t.Fatalf("pan did not take: crop y=%d", y)
	}

	m.Update(cssFrame(t, m, loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p>v2 (script mutated the page)</p>`)}))
	if y := m.viewer.PaintViewport().Origin.Y; y != 900 {
		t.Fatalf("re-flow lost the scroll offset: crop y=%d", y)
	}
	if m.viewer.Result().Document() == first.Document() {
		t.Fatal("viewer still shows the old Result")
	}

	// A stale frame (older generation) never regresses the view.
	m.Update(cssFrame(t, m, loop.FrameMsg{Gen: 1, Result: first}))
	if m.viewer.Result().Document() == first.Document() {
		t.Fatal("stale frame was applied")
	}

	// New page: pageLoadedMsg precedes its first frame.
	m.Update(pageLoadedMsg{html: "<p>next</p>", base: mustURL(t, "https://example.test/next"), seq: m.navSeq, hist: pushNew})
	m.Update(cssFrame(t, m, loop.FrameMsg{Gen: 3, Result: renderTall(t, `<p>next page</p>`)}))
	if y := m.viewer.PaintViewport().Origin.Y; y != 0 {
		t.Fatalf("new page did not start at the top: crop y=%d", y)
	}
	if m.hIndex != 0 || len(m.history) != 1 {
		t.Fatalf("history not pushed: %v @%d", m.history, m.hIndex)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Synthetic UI fixtures use the same atomic frame contract as the loop worker.
func cssFrame(t *testing.T, m *Model, f loop.FrameMsg) loop.FrameMsg {
	t.Helper()
	if f.Err != nil || f.Result == nil || f.Chunk.Count > 0 {
		return f
	}
	v := nthtml.PaintViewport{Cols: m.cols, Rows: m.viewRows()}
	if m.viewer != nil {
		v = m.viewer.PaintViewport()
	}
	if m.resetView {
		v.Origin.X = 0
		v.Origin.Y = 0
		v.ZoomSteps = 0
	}
	plan, err := nthtml.PlanPaint(f.Result, v)
	if err != nil {
		t.Fatal(err)
	}
	img, err := f.Result.Paint(context.Background(), plan.Area, plan.Scale)
	if err != nil {
		t.Fatal(err)
	}
	f.Painted = &nthtml.PaintedResult{Result: f.Result, Revision: f.Gen, Plan: plan, Image: img}
	return f
}
