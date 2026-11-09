// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml/document"
	"nimbleterminal.dev/ntpicture"

	"nimbleterminal.dev/ntbrowser/loop"
)

// The raster budget is three screens of terminal pixels, at most 12 Mpx, never
// less than the viewport, and never more than ntpicture allows in one image.
func TestRasterBudgetBoundsBand(t *testing.T) {
	cases := []struct {
		cols, rows, cw, ch, want int
	}{
		{180, 58, 10, 20, 3 * 180 * 10 * 58 * 20},        // 2.1 Mpx viewport: three screens
		{220, 68, 18, 36, 12 << 20},                      // 9.7 Mpx viewport: the 12 Mpx cap, above one screen
		{400, 100, 18, 36, 400 * 18 * 100 * 36},          // 25.9 Mpx viewport: one screen, over the cap
		{80, 24, 1, 2, 3 * 80 * 24 * 2},                  // glyph cells
		{1000, 1000, 100, 100, ntpicture.MaxImagePixels}, // beyond one image
		{0, 0, 0, 0, 3},
	}
	for _, c := range cases {
		if got := rasterBudget(c.cols, c.rows, c.cw, c.ch); got != c.want {
			t.Errorf("rasterBudget(%d,%d,%d,%d) = %d, want %d", c.cols, c.rows, c.cw, c.ch, got, c.want)
		}
	}
}

// A terminal whose probe has already reported Kitty and its cell size gets a
// first band painted at that geometry, bounded to the raster budget, instead of
// a glyph-resolution band the transport would upscale tenfold; the viewer's
// picture carries the same budget and follows a resize.
func TestFirstFrameUsesProbedCellSizeAndRasterBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chan tea.Msg, 256)
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, chanSink(sink))
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), "")
	m.session = ntpicture.NewTerminalSession(ntpicture.TerminalOptions{Kitty: ntpicture.KittyCapabilitySupported, CellPixelWidth: 10, CellPixelHeight: 20})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	l.LoadPage(`<body>`+strings.Repeat(`<p style="height:1000px">long CSS text</p>`, 40), "https://example.test/", nil)
	first := chunkAppFrame(t, m, sink)
	if first.Err != nil || first.Painted == nil {
		t.Fatalf("missing CSS band: %v", first.Err)
	}
	plan := first.Painted.Plan
	if plan.CellWidth != 10 || plan.CellHeight != 20 {
		t.Fatalf("first band painted at %dx%d px cells, want the probed 10x20: %+v", plan.CellWidth, plan.CellHeight, plan)
	}
	rows := m.viewRows()
	if plan.BandRows > bandScreens*rows || plan.BandRows < rows {
		t.Fatalf("band of %d rows for a %d-row viewport, want between one and two screens", plan.BandRows, rows)
	}
	if want := rasterBudget(80, rows, 10, 20); m.viewer.Picture().MaxRasterPixels() != want {
		t.Fatalf("raster budget %d not applied to the picture: %d", want, m.viewer.Picture().MaxRasterPixels())
	}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(m, cmd)
	if want := rasterBudget(120, m.viewRows(), 10, 20); m.viewer.Picture().MaxRasterPixels() != want {
		t.Fatalf("picture budget after resize %d, want %d", m.viewer.Picture().MaxRasterPixels(), want)
	}
}
