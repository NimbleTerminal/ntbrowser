// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"image"
	"image/color"
	"nimbleterminal.dev/nthtml/document"
	"strings"
	"testing"
	"time"
)

func TestHoverUsesPresentedGenerationAndClears(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 128)
	l := New(ctx, Config{Profile: testProfile, Width: 400, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(`<style>a{color:black}a:hover{color:red}</style><a href="/x">hover target</a>`, "https://example.test/", nil)
	f := nextFrame(t, sink)
	area := f.Result.Links[0].Regions[0]
	l.HoverAtFrame(f.Gen, area)
	hasColor := func(f FrameMsg, want color.RGBA) bool {
		for _, b := range f.Result.Boxes {
			if b.Tag == "text" && strings.Contains(b.Text, "hover") && b.Color == want {
				return true
			}
		}
		return false
	}
	hovered := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return hasColor(f, color.RGBA{255, 0, 0, 255}) })
	l.HoverAtFrame(f.Gen, image.Rectangle{}) // stale input must not clear current hover
	l.HoverAtFrame(hovered.Gen, image.Rectangle{})
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return hasColor(f, color.RGBA{0, 0, 0, 255}) })
}
