// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"image/color"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml/document"
)

func textColor(res *document.Result, text string) color.RGBA {
	for _, b := range res.Boxes {
		if b.Text == text {
			return b.Color
		}
	}
	return color.RGBA{}
}

// TestSetColorSchemeReflows: the page lays out light until the host reports a
// dark terminal, then re-flows with the dark branch of light-dark() and the
// prefers-color-scheme media query.
func TestSetColorSchemeReflows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: testProfile, Width: 400, MinFrame: time.Millisecond}, sink)
	go l.Run()
	if l.ColorScheme() != "light" {
		t.Fatalf("default scheme %q", l.ColorScheme())
	}
	l.LoadPage(`<style>p{color:light-dark(#ff0000,#0000ff)} @media (prefers-color-scheme: dark){em{color:#00ff00}}</style><p>t</p><em>e</em>`, "https://example.test/", nil)
	first := waitFor(t, sink, func(m tea.Msg) bool { f, ok := m.(FrameMsg); return ok && f.Result != nil }).(FrameMsg)
	if c := textColor(first.Result, "t"); c != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("light scheme colour %v", c)
	}
	l.SetColorScheme("dark")
	dark := waitFor(t, sink, func(m tea.Msg) bool {
		f, ok := m.(FrameMsg)
		return ok && f.Result != nil && textColor(f.Result, "t") == (color.RGBA{0, 0, 255, 255})
	}).(FrameMsg)
	if c := textColor(dark.Result, "e"); c != (color.RGBA{0, 255, 0, 255}) {
		t.Fatalf("prefers-color-scheme: dark not applied: %v", c)
	}
	if l.ColorScheme() != "dark" {
		t.Fatalf("scheme after switch %q", l.ColorScheme())
	}
	l.SetColorScheme("sepia") // ignored
	if l.ColorScheme() != "dark" {
		t.Fatal("unknown scheme accepted")
	}
}
