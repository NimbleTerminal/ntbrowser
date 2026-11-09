// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
	"nimbleterminal.dev/ntpicture"

	"nimbleterminal.dev/ntbrowser/loop"
)

func fillModel(t *testing.T, cols, rows, cw, ch int, page string) (*Model, chan tea.Msg) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chan tea.Msg, 256)
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 800, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, chanSink(sink))
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), "")
	m.session = ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilitySupported, CellPixelWidth: cw, CellPixelHeight: ch})
	_, cmd := m.Update(tea.WindowSizeMsg{Width: cols, Height: rows})
	drive(m, cmd)
	l.LoadPage(page, "https://example.test/", nil)
	// The first band is laid out at the loop's starting width; the re-flow at
	// the terminal's width follows.
	for i := 0; ; i++ {
		f := chunkAppFrame(t, m, sink)
		if f.Err == nil && f.Painted != nil && m.viewer != nil && f.Result.Bounds().Dx() == documentWidth(m.viewer) {
			break
		}
		if i == 3 || f.Err != nil || f.Painted == nil {
			bounds, mode := "", ""
			if m.viewer != nil {
				bounds, mode = m.viewer.Bounds().String(), strconv.Itoa(int(m.viewer.Picture().Mode()))
			}
			t.Fatalf("frame %d: %v (layoutWidth %d, pictureCols %d, bounds %s, mode %s)", i, f.Err, m.layoutWidth, m.pictureCols(), bounds, mode)
		}
	}
	return m, sink
}

// A terminal wider than the Kitty placeholder grid still shows the page: the
// viewer spans the columns the protocol can address and the page is laid out
// at exactly that width, instead of a frame the transport refuses.
func TestWideTerminalStaysWithinKittyPlaceholderGrid(t *testing.T) {
	limit := nthtml.MaxKittyPlaceholderCells()
	cols, cw := limit+17, 14
	m, _ := fillModel(t, cols, 40, cw, 28, `<body style="margin:0">`+strings.Repeat(`<p style="height:1000px">long CSS text</p>`, 20))
	if got := m.viewer.Bounds().Dx(); got != limit {
		t.Fatalf("viewer spans %d columns, want the %d-column placeholder grid", got, limit)
	}
	if m.layoutWidth != limit*cw || documentWidth(m.viewer) != limit*cw {
		t.Fatalf("layout width %d (documentWidth %d), want %d", m.layoutWidth, documentWidth(m.viewer), limit*cw)
	}
	if m.viewer.PaintViewport().Cols != limit {
		t.Fatalf("paint viewport plans %d columns", m.viewer.PaintViewport().Cols)
	}
	body := m.viewer.View()
	if n := strings.Count(body, "\n") + 1; n != m.viewRows() {
		t.Fatalf("viewer shows %d rows of %d: the frame was refused", n, m.viewRows())
	}
	if n := strings.Count(m.View().Content, "\n") + 1; n != 40 {
		t.Fatalf("app view has %d rows, want 40", n)
	}
}

// A page shorter than the terminal does not pull the status lines up after
// it: the view keeps the terminal's height while loading and once drawn.
func TestShortPageKeepsStatusLinesAtBottom(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chan tea.Msg, 256)
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 800, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, chanSink(sink))
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), "")
	m.session = ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilitySupported, CellPixelWidth: 10, CellPixelHeight: 20})
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	drive(m, cmd)
	if n := strings.Count(m.View().Content, "\n") + 1; n != 26 {
		t.Fatalf("view while loading has %d rows, want 26", n)
	}
	l.LoadPage(`<body style="margin:0;background:#102030"><p>short</p></body>`, "https://example.test/", nil)
	f := chunkAppFrame(t, m, sink)
	if f.Err != nil || f.Painted == nil {
		t.Fatalf("missing frame: %v", f.Err)
	}
	if rows := f.Painted.Plan.Rows; rows >= m.viewRows() {
		t.Fatalf("test page fills %d rows; it must be shorter than the %d-row viewer", rows, m.viewRows())
	}
	view := m.View().Content
	if n := strings.Count(view, "\n") + 1; n != 26 {
		t.Fatalf("view with a short page has %d rows, want 26", n)
	}
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[m.viewRows()-1], "\x1b[48;2;16;32;48m") {
		t.Fatalf("row above the status line is not canvas-coloured: %q", lines[m.viewRows()-1])
	}
	if !strings.Contains(lines[25], "q quit") {
		t.Fatalf("help line is not the last row: %q", lines[25])
	}
}
