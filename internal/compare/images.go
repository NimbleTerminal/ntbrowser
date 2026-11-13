// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package compare

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/net/html"
	"nimbleterminal.dev/nthtml/document"
)

type PageImage struct {
	URL, ID, SourceID string
	Rect              Rect
	Loaded            bool
	Status, Note      string
}
type ImageComparison struct {
	URL, Status, Note               string
	Glynx, Chrome                   *PageImage `json:",omitempty"`
	GlynxCrop, ChromeCrop, DiffCrop string     `json:",omitempty"`
	DifferentPixels                 int
	PixelDifferencePercent          float64
}

// Immutable serializer snapshots are correlated with the exact raster revision.
// Keep only two: an old/current paint plus the latest pending layout.
type imageSnapshots struct {
	sync.Mutex
	frames map[uint64]string
}

func (s *imageSnapshots) record(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	top, ok := m["top_level"].(bool)
	if !ok || !top {
		return
	}
	gen, ok := m["frame"].(uint64)
	if !ok {
		return
	}
	markup, ok := m["html"].(string)
	if !ok {
		return
	}
	s.Lock()
	defer s.Unlock()
	if s.frames == nil {
		s.frames = map[uint64]string{}
	}
	s.frames[gen] = markup
	if len(s.frames) > 2 {
		old := gen
		for n := range s.frames {
			if n < old {
				old = n
			}
		}
		delete(s.frames, old)
	}
}
func (s *imageSnapshots) get(gen uint64) string { s.Lock(); defer s.Unlock(); return s.frames[gen] }

// Match img boxes using the nearest serialized source anchor. Never infer a
// URL from box order: hidden siblings, flex ordering and duplicated sources can
// make order-based matching silently attribute a defect to the wrong asset.
func glynxImages(markup, base string, boxes []document.Box, loaded map[string]image.Image) []PageImage {
	root, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var images []PageImage
	var walk func(*html.Node, string)
	walk = func(n *html.Node, source string) {
		attr := func(name string) string {
			for _, a := range n.Attr {
				if a.Key == name {
					return a.Val
				}
			}
			return ""
		}
		if id := attr("id"); id != "" {
			source = id
		} else if n.Data == "a" && attr("name") != "" {
			source = attr("name")
		}
		if n.Type == html.ElementNode && n.Data == "img" && len(images) < 128 {
			src := attr("src")
			if src != "" && len(src) <= 4096 {
				if ref, e := url.Parse(src); e == nil {
					images = append(images, PageImage{URL: u.ResolveReference(ref).String(), ID: attr("id"), SourceID: source, Loaded: loaded[src] != nil, Status: "UNAVAILABLE", Note: "No unambiguous painted image box for this source anchor."})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, source)
		}
	}
	walk(root, "")
	count := map[string]int{}
	for _, im := range images {
		count[im.SourceID]++
	}
	for i := range images {
		im := &images[i]
		if count[im.SourceID] != 1 {
			im.Note = "Multiple images share this source anchor; box order is not guessed."
			continue
		}
		var candidates []document.Box
		for _, b := range boxes {
			if b.Tag == "img" && b.SourceID == im.SourceID && !b.Bounds.Empty() {
				candidates = append(candidates, b)
			}
		}
		if len(candidates) != 1 {
			continue
		}
		b := candidates[0]
		im.Rect = rect(b.Bounds)
		im.Status = "LOCATED"
		im.Note = ""
		if !b.Clip.Empty() && !b.Bounds.In(b.Clip) {
			im.Status = "CLIPPED"
			im.Note = "Image is clipped; full-image pixel comparison unavailable."
		}
	}
	return images
}
func imageBounds(r Rect) image.Rectangle {
	return image.Rect(int(math.Floor(r.X)), int(math.Floor(r.Y)), int(math.Ceil(r.X+r.Width)), int(math.Ceil(r.Y+r.Height)))
}
func imageCrop(src image.Image, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), src, r.Min, draw.Src)
	return out
}
func readScreenshot(dir, name string) (image.Image, error) {
	f, e := os.Open(filepath.Join(dir, name))
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return png.Decode(f)
}

// Compare screenshot crops at their own page positions. This removes global
// layout shifts without rescaling the evidence or pretending that different
// rendered sizes are pixel-equivalent. CSS/background/occlusion still matter.
func compareImages(dir string, a, b Capture) ([]ImageComparison, error) {
	as, bs := map[string][]PageImage{}, map[string][]PageImage{}
	urls := map[string]bool{}
	for _, im := range a.Images {
		as[im.URL] = append(as[im.URL], im)
		urls[im.URL] = true
	}
	for _, im := range b.Images {
		bs[im.URL] = append(bs[im.URL], im)
		urls[im.URL] = true
	}
	keys := make([]string, 0, len(urls))
	for u := range urls {
		keys = append(keys, u)
	}
	sort.Strings(keys)
	ai, e := readScreenshot(dir, a.Screenshot)
	if e != nil {
		return nil, e
	}
	bi, e := readScreenshot(dir, b.Screenshot)
	if e != nil {
		return nil, e
	}
	var rows []ImageComparison
	budget := 8 << 20
	for i, u := range keys {
		row := ImageComparison{URL: u, Status: "UNAVAILABLE"}
		aa, bb := as[u], bs[u]
		if len(aa) != 1 || len(bb) != 1 {
			row.Note = "Requires one occurrence of this resource URL in each engine; missing or repeated URLs are not guessed."
			rows = append(rows, row)
			continue
		}
		row.Glynx = &aa[0]
		row.Chrome = &bb[0]
		if aa[0].Status != "LOCATED" || bb[0].Status != "LOCATED" || !aa[0].Loaded || !bb[0].Loaded {
			row.Note = "One image is unloaded, hidden, clipped, or cannot be associated with a painted box."
			rows = append(rows, row)
			continue
		}
		ar, br := imageBounds(aa[0].Rect), imageBounds(bb[0].Rect)
		if ar.Empty() || br.Empty() || !ar.In(ai.Bounds()) || !br.In(bi.Bounds()) {
			row.Status = "OUTSIDE_VIEWPORT"
			row.Note = "Both complete image rectangles must lie inside the captured viewport."
			rows = append(rows, row)
			continue
		}
		cost := ar.Dx()*ar.Dy() + br.Dx()*br.Dy() + max(ar.Dx()*ar.Dy(), br.Dx()*br.Dy())
		if cost > budget {
			row.Note = "Image crop budget exhausted (8 million pixels)."
			rows = append(rows, row)
			continue
		}
		budget -= cost
		ac, bc := imageCrop(ai, ar), imageCrop(bi, br)
		row.GlynxCrop = fmt.Sprintf("image-%03d-glynx.png", i)
		row.ChromeCrop = fmt.Sprintf("image-%03d-chrome.png", i)
		if e = writePNG(filepath.Join(dir, row.GlynxCrop), ac); e != nil {
			return nil, e
		}
		if e = writePNG(filepath.Join(dir, row.ChromeCrop), bc); e != nil {
			return nil, e
		}
		if ar.Size() != br.Size() {
			row.Status = "SIZE_DIFFERS"
			row.Note = "Different crop dimensions; shown at native size without resampling."
			rows = append(rows, row)
			continue
		}
		diff := image.NewRGBA(ac.Bounds())
		for y := 0; y < ar.Dy(); y++ {
			for x := 0; x < ar.Dx(); x++ {
				if color.NRGBAModel.Convert(ac.At(x, y)) != color.NRGBAModel.Convert(bc.At(x, y)) {
					row.DifferentPixels++
					diff.Set(x, y, color.RGBA{255, 0, 255, 255})
				} else {
					diff.Set(x, y, color.White)
				}
			}
		}
		row.PixelDifferencePercent = 100 * float64(row.DifferentPixels) / float64(ar.Dx()*ar.Dy())
		row.Status = "PIXELS_MATCH"
		if row.DifferentPixels > 0 {
			row.Status = "PIXELS_DIFFER"
		}
		row.Note = "URL-matched screenshot crops, aligned locally. Differences may include SVG rendering, CSS, backgrounds, occlusion, antialiasing, or live resource changes. Inspect these crops, then test the asset in isolation."
		row.DiffCrop = fmt.Sprintf("image-%03d-diff.png", i)
		if e = writePNG(filepath.Join(dir, row.DiffCrop), diff); e != nil {
			return nil, e
		}
		rows = append(rows, row)
	}
	// Put actionable visible differences before offscreen assets.
	rank := func(status string) int {
		switch status {
		case "PIXELS_DIFFER":
			return 0
		case "SIZE_DIFFERS":
			return 1
		case "PIXELS_MATCH":
			return 2
		case "UNAVAILABLE":
			return 3
		default:
			return 4
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rank(rows[i].Status) < rank(rows[j].Status) })
	return rows, nil
}
