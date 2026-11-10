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

func fillModel(t *testing.T, cols, rows, cw, ch int, page string) (*Model, loop.FrameMsg) {
	return fillModelScaled(t, cols, rows, cw, ch, 0, page)
}

// fillModelScaled is fillModel with a fixed --scale (0 for auto).
func fillModelScaled(t *testing.T, cols, rows, cw, ch int, scale float64, page string) (*Model, loop.FrameMsg) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chan tea.Msg, 256)
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 800, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, chanSink(sink))
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), Options{})
	m.pageScale = scale
	m.session = ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilitySupported, CellPixelWidth: cw, CellPixelHeight: ch})
	_, cmd := m.Update(tea.WindowSizeMsg{Width: cols, Height: rows})
	drive(m, cmd)
	l.LoadPage(page, "https://example.test/", nil)
	// The first band is laid out at the loop's starting width; the re-flow at
	// the terminal's width follows.
	for i := 0; ; i++ {
		f := chunkAppFrame(t, m, sink)
		if f.Err == nil && f.Painted != nil && m.viewer != nil && f.Result.Bounds().Dx() == m.documentWidth() {
			return m, f
		}
		if i == 3 || f.Err != nil || f.Painted == nil {
			bounds, mode := "", ""
			if m.viewer != nil {
				bounds, mode = m.viewer.Bounds().String(), strconv.Itoa(int(m.viewer.Picture().Mode()))
			}
			t.Fatalf("frame %d: %v (layoutWidth %d, pictureCols %d, bounds %s, mode %s)", i, f.Err, m.layoutWidth, m.pictureCols(), bounds, mode)
		}
	}
}

// HiDPI cells get a device scale: the page is laid out at half the device
// pixels and painted at 2x, so its text is the size a browser gives it on a
// retina display; 1x cells stay 1:1, and --scale overrides the choice. The
// loop reports the scale to scripts.
func TestHiDPICellsLayOutAtDeviceScale(t *testing.T) {
	page := `<body style="margin:0">` + strings.Repeat(`<p style="height:1000px">long CSS text</p>`, 20)
	m, f := fillModel(t, 200, 40, 19, 37, page)
	if m.deviceScale() != 2 || m.layoutWidth != 1900 || f.Painted.Plan.Scale != 2 || f.Painted.Image.Bounds().Dx() != 3800 {
		t.Fatalf("19x37 cells: scale %g, layout %d px, plan scale %g, band %d px; want 2, 1900, 2, 3800", m.deviceScale(), m.layoutWidth, f.Painted.Plan.Scale, f.Painted.Image.Bounds().Dx())
	}
	if st := m.loop.Scale(); st != 2 {
		t.Fatalf("loop scale %g, want 2", st)
	}
	m, f = fillModel(t, 200, 40, 10, 20, page)
	if m.deviceScale() != 1 || m.layoutWidth != 2000 || f.Painted.Plan.Scale != 1 {
		t.Fatalf("10x20 cells: scale %g, layout %d px, plan scale %g; want 1, 2000, 1", m.deviceScale(), m.layoutWidth, f.Painted.Plan.Scale)
	}
	m, f = fillModelScaled(t, 200, 40, 19, 37, 1, page)
	if m.deviceScale() != 1 || m.layoutWidth != 3800 || f.Painted.Plan.Scale != 1 {
		t.Fatalf("--scale 1 on 19x37 cells: scale %g, layout %d px, plan scale %g; want 1, 3800, 1", m.deviceScale(), m.layoutWidth, f.Painted.Plan.Scale)
	}
	// An odd device width rounds the CSS width up, so the band is cropped by a
	// pixel rather than stretched.
	m, f = fillModelScaled(t, 201, 40, 19, 37, 2, page)
	if m.layoutWidth != 1910 || f.Painted.Image.Bounds().Dx() < 201*19 {
		t.Fatalf("201x19 px at 2x: layout %d (want 1910), band %d px (want at least %d)", m.layoutWidth, f.Painted.Image.Bounds().Dx(), 201*19)
	}
}

// A terminal wider than the Kitty placeholder grid still shows the page: the
// viewer spans the columns the protocol can address and the page is laid out
// at exactly that width, instead of a frame the transport refuses.
func TestWideTerminalStaysWithinKittyPlaceholderGrid(t *testing.T) {
	limit := nthtml.MaxKittyPlaceholderCells()
	cols, cw := limit+17, 14
	m, _ := fillModel(t, cols, 40, cw, 28, `<body style="margin:0">`+strings.Repeat(`<p style="height:1000px">long CSS text</p>`, 20))
	if m.deviceScale() != 2 {
		t.Fatalf("28 px cells should give a 2x scale, got %g", m.deviceScale())
	}
	if got := m.viewer.Bounds().Dx(); got != limit {
		t.Fatalf("viewer spans %d columns, want the %d-column placeholder grid", got, limit)
	}
	if m.layoutWidth != limit*cw/2 || m.documentWidth() != limit*cw/2 {
		t.Fatalf("layout width %d (documentWidth %d), want %d", m.layoutWidth, m.documentWidth(), limit*cw/2)
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
	m := New(l, testProfile(nil, false, NavFree), Options{})
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
