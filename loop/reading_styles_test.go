// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"

	"nimbleterminal.dev/nthtml/document"
	"nimbleterminal.dev/nttypeset"
)

// Exercise the host's actual chunk conversion/raster/source-map path, not just
// the upstream AST. Distinct image/cell colors expose dropped content/styles.
func TestReadingStylesAcrossEvictionAndResize(t *testing.T) {
	ctx := context.Background()
	red := color.RGBA{237, 11, 29, 255}
	yellow := color.RGBA{245, 210, 9, 255}
	blue := color.RGBA{32, 80, 208, 255}
	asset := func(c color.RGBA) *image.RGBA {
		im := image.NewRGBA(image.Rect(0, 0, 200, 100))
		draw.Draw(im, im.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
		return im
	}
	html := `<body><section id="callout" style="line-height:48px;border:2px solid #345;padding:8px;background:#def"><h2 style="font-weight:normal">Callout</h2><p>Before the figure</p><pre style="margin:0">Code sample</pre></section>
 <p id="figure"><a id="photo-link" href="/photo"><img id="photo" src="photo.png" alt="Visible photograph" style="max-width:80px;max-height:30px"></a></p>
 <p><a href="/hidden" style="display:none"><img src="hidden.png" alt="SECRET"></a></p>
 <table id="data" style="border-collapse:separate;border-spacing:0"><tr><th style="vertical-align:top;font-weight:normal;background:#2050d0;padding:6px;border:2px solid #123">Name</th><td style="vertical-align:top;text-align:right;padding:10px">Value</td></tr></table>` + strings.TrimSuffix(strings.TrimPrefix(chunkHistory(80), "<body>"), "</body>") + `</body>`
	doc, err := document.Parse(ctx, strings.NewReader(html), document.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{400, 260} {
		opts := document.Options{Width: width, Fonts: document.MinimalFonts, Images: map[string]image.Image{"photo.png": asset(red), "hidden.png": asset(yellow)}}
		r, err := newChunkRender(ctx, doc, opts)
		if err != nil {
			t.Fatal(err)
		}
		figureIndex, _, ok := r.source.ChunkRange("#figure")
		if !ok {
			t.Fatal("figure range missing")
		}
		// Distant visits evict the figure; all IDs must survive reconstruction.
		for _, anchor := range []string{"#figure", "#data", "#message-0", "#message-30", "#message-70", "#callout", "#photo", "#photo-link"} {
			res, info, err := r.render(ctx, chunkRequest{anchor: anchor}, opts)
			if err != nil {
				t.Fatal(err)
			}
			if info.Anchor != anchor {
				t.Fatalf("lost fragment %s", anchor)
			}
			if len(r.source.maps) > 2 {
				t.Fatal("source maps exceeded cache bound")
			}
			if !colorBounds(res.Image, yellow).Empty() {
				t.Fatal("hidden image painted")
			}
			for _, link := range res.Links {
				if link.Href == "/hidden" {
					t.Fatal("hidden link survived")
				}
			}
			chunk, err := r.cache.At(ctx, info.Index, width, nttypeset.LayoutOptions{})
			if err != nil {
				t.Fatal(err)
			}
			switch anchor {
			case "#figure", "#photo", "#photo-link":
				if got := colorBounds(res.Image, red).Size(); got != image.Pt(60, 30) {
					t.Fatalf("width %d image size %v", width, got)
				}
				for _, id := range []string{"figure", "photo-link", "photo"} {
					if _, ok := res.Anchors[id]; !ok {
						t.Fatalf("lost image identity %s", id)
					}
				}
			case "#message-70":
				if _, cached := r.source.maps[figureIndex]; cached {
					t.Fatal("fixture did not evict the figure")
				}
			case "#data":
				if colorBounds(res.Image, blue).Empty() {
					t.Fatal("table cell background lost")
				}
				text, err := res.TTY(ctx, 60)
				if err != nil || !strings.Contains(text, "Name") || !strings.Contains(text, "Value") {
					t.Fatalf("table copy text %q %v", text, err)
				}
			case "#callout":
				found := false
				for _, b := range chunk.Document.Blocks {
					if b.Kind == nttypeset.KindHeading {
						found = true
						if b.Format == nil || b.Format.LineHeight != 48 {
							t.Fatal("heading lost inherited line height")
						}
						for _, span := range b.Spans {
							if span.Style&nttypeset.Bold != 0 {
								t.Fatal("heading ignored normal weight")
							}
						}
					}
				}
				if !found {
					t.Fatal("callout heading missing")
				}
			}
		}
	}
}

func colorBounds(im *image.RGBA, c color.RGBA) image.Rectangle {
	var bounds image.Rectangle
	for y := im.Bounds().Min.Y; y < im.Bounds().Max.Y; y++ {
		for x := im.Bounds().Min.X; x < im.Bounds().Max.X; x++ {
			if im.RGBAAt(x, y) == c {
				bounds = bounds.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return bounds
}

// Heading inheritance is shared with the default CSS renderer, not only the
// reading adapter. Check global layout geometry through the actual loop worker.
func TestCSSInheritedHeadingLineHeight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	l := New(ctx, Config{Width: 400, Fonts: document.MinimalFonts}, sink)
	go l.Run()
	l.LoadPage(`<div style="line-height:48px"><h2>MMMM<br>MMMM</h2></div>`, "https://example.test/", nil)
	f := nextFrame(t, sink)
	if f.Err != nil || f.Painted == nil || f.Result.Image != nil {
		t.Fatalf("CSS viewport frame failed: %v", f.Err)
	}
	var tops []int
	for _, box := range f.Result.Boxes {
		if box.Text == "MMMM" {
			tops = append(tops, box.Bounds.Min.Y)
		}
	}
	if len(tops) != 2 || tops[1]-tops[0] != 48 {
		t.Fatalf("inherited heading line positions: %v", tops)
	}
}
