package app

import (
	"context"
	"image"
	"strings"
	"testing"

	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
)

const cellPage = `<!doctype html><html><body style="font-size:16px">
<p><a href="/one">first link on the first line</a></p>
<p style="text-align:right"><a href="/two">right-aligned</a></p>
<div style="height:600px"></div>
<p><a href="/three">a link far below the fold</a> plain text after it</p>
<div style="height:600px"></div>
</body></html>`

// The host's cell→document mapping must agree with nthtml's own, which is
// only reachable through Model.LinkAtCell: wherever nthtml reports a link
// under a cell, the host's area overlaps that link's regions, and wherever
// the host's area overlaps a link region, nthtml reports a link. Checked at
// a non-zero widget origin, and again after scrolling.
func TestCellToDocumentAgreesWithLinkAtCell(t *testing.T) {
	res, err := document.Render(context.Background(), strings.NewReader(cellPage), document.Options{Width: 400})
	if err != nil {
		t.Fatal(err)
	}
	m := nthtml.New(res)
	_ = m.Init()
	_ = m.SetBounds(3, 2, 80, 24)
	overlaps := func(area image.Rectangle, l document.Link) bool {
		for _, r := range l.Regions {
			if !r.Intersect(area).Empty() {
				return true
			}
		}
		return false
	}
	check := func(label string) (hits int) {
		for y := 0; y < 30; y++ {
			for x := 0; x < 90; x++ {
				p := image.Pt(x, y)
				link, hasLink := m.LinkAtCell(p)
				area, ok := cellToDocument(m, p)
				if hasLink {
					hits++
					if !ok || !overlaps(area, link) {
						t.Fatalf("%s: cell %v: nthtml sees %q, host area %v (ok=%v) misses it", label, p, link.Href, area, ok)
					}
					continue
				}
				if !ok {
					continue
				}
				for _, l := range res.Links {
					if overlaps(area, l) {
						t.Fatalf("%s: cell %v: host area %v overlaps %q but nthtml sees no link", label, p, area, l.Href)
					}
				}
			}
		}
		return hits
	}
	if n := check("top"); n == 0 {
		t.Fatal("no link cells at the top of the page; test is vacuous")
	}
	_ = m.Pan(0, 700)
	if n := check("scrolled"); n == 0 {
		t.Fatal("no link cells after scrolling; test is vacuous")
	}
	if _, ok := cellToDocument(m, image.Pt(0, 0)); ok {
		t.Fatal("cell outside the widget origin mapped to a document area")
	}
	if w := documentWidth(m); w != 800 {
		t.Fatalf("glyph-fallback geometry should give 10px per column (800), got %d", w)
	}
}
