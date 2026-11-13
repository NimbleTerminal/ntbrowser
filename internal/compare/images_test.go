// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package compare

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"

	"nimbleterminal.dev/nthtml/document"
)

func TestImagesWithoutIDsUseSnapshotAnchors(t *testing.T) {
	markup := `<a id="nt-h-1"><img src="logo.svg"><span>Brand</span></a><div id="many"><img src="a.svg"><img src="b.svg"></div><img id="failed" src="failed.svg">`
	boxes := []document.Box{{Tag: "img", SourceID: "nt-h-1", Bounds: image.Rect(12, 0, 92, 80)}, {Tag: "img", SourceID: "many", Bounds: image.Rect(100, 0, 120, 20)}, {Tag: "img", SourceID: "failed", Bounds: image.Rect(140, 0, 160, 20)}}
	got := glynxImages(markup, "https://example.test/page", boxes, map[string]image.Image{"logo.svg": image.NewRGBA(image.Rect(0, 0, 80, 80))})
	if len(got) != 4 || got[0].URL != "https://example.test/logo.svg" || got[0].Status != "LOCATED" || got[0].ID != "" || !got[0].Loaded {
		t.Fatalf("missing anonymous logo: %+v", got)
	}
	if got[1].Status != "UNAVAILABLE" || got[2].Status != "UNAVAILABLE" {
		t.Fatal("guessed sibling image order")
	}
	if got[3].Loaded {
		t.Fatal("placeholder claimed as a loaded image")
	}
}
func TestImageSnapshotIsBoundedAndRealmBound(t *testing.T) {
	var s imageSnapshots
	for i := uint64(1); i <= 3; i++ {
		s.record(map[string]any{"top_level": true, "frame": i, "html": "main"})
	}
	s.record(map[string]any{"top_level": false, "frame": uint64(3), "html": "child"})
	if s.get(1) != "" || s.get(2) != "main" || s.get(3) != "main" {
		t.Fatal("incorrect snapshot retention or realm isolation")
	}
}
func TestImageCropsIgnorePageShiftAndExposeLostDetail(t *testing.T) {
	for _, hole := range []bool{false, true} {
		t.Run(map[bool]string{false: "same", true: "lost hole"}[hole], func(t *testing.T) {
			dir := t.TempDir()
			a := image.NewRGBA(image.Rect(0, 0, 100, 80))
			b := image.NewRGBA(a.Bounds())
			draw.Draw(a, a.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			draw.Draw(b, b.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			ar, br := image.Rect(5, 5, 25, 25), image.Rect(60, 40, 80, 60)
			draw.Draw(a, ar, image.NewUniform(color.Black), image.Point{}, draw.Src)
			draw.Draw(b, br, image.NewUniform(color.Black), image.Point{}, draw.Src)
			if hole {
				draw.Draw(b, image.Rect(65, 45, 75, 55), image.NewUniform(color.White), image.Point{}, draw.Src)
			}
			if e := writePNG(filepath.Join(dir, "a.png"), a); e != nil {
				t.Fatal(e)
			}
			if e := writePNG(filepath.Join(dir, "b.png"), b); e != nil {
				t.Fatal(e)
			}
			obs := func(r image.Rectangle) PageImage {
				return PageImage{URL: "https://example.test/logo.svg", Status: "LOCATED", Loaded: true, Rect: rect(r)}
			}
			rows, e := compareImages(dir, Capture{Screenshot: "a.png", Images: []PageImage{obs(ar)}}, Capture{Screenshot: "b.png", Images: []PageImage{obs(br)}})
			if e != nil {
				t.Fatal(e)
			}
			want := "PIXELS_MATCH"
			if hole {
				want = "PIXELS_DIFFER"
			}
			if len(rows) != 1 || rows[0].Status != want {
				t.Fatalf("bad image comparison: %+v", rows)
			}
			if hole && rows[0].DifferentPixels != 100 {
				t.Fatalf("lost detail: %d pixels", rows[0].DifferentPixels)
			}
		})
	}
}
func TestRepeatedImageURLsRemainUnavailable(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for _, name := range []string{"a.png", "b.png"} {
		if e := writePNG(filepath.Join(dir, name), img); e != nil {
			t.Fatal(e)
		}
	}
	im := PageImage{URL: "https://example.test/repeated.svg", Status: "LOCATED", Loaded: true, Rect: rect(img.Bounds())}
	rows, e := compareImages(dir, Capture{Screenshot: "a.png", Images: []PageImage{im, im}}, Capture{Screenshot: "b.png", Images: []PageImage{im}})
	if e != nil || len(rows) != 1 || rows[0].Status != "UNAVAILABLE" || rows[0].GlynxCrop != "" {
		t.Fatalf("guessed repeated URL: %+v %v", rows, e)
	}
}
