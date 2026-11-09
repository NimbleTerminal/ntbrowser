// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"image"
	"math"

	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
	"nimbleterminal.dev/ntpicture"
)

// cellToDocument delegates to nthtml's displayed-band mapping.
func cellToDocument(m *nthtml.Model, p image.Point) (image.Rectangle, bool) {
	if m == nil {
		return image.Rectangle{}, false
	}
	return m.CellToDocument(p)
}

// documentWidth is the CSS-pixel width the loop should lay pages out at for
// this viewer at the given device pixel ratio. It uses terminal cell metrics
// rather than a resident band's raster dimensions, or ten pixels per column in
// glyph fallback, clamped to nthtml's width bounds. The viewer's columns are
// already bounded by the Kitty placeholder grid (pictureCols), so the layout
// width matches the band the transport can show cell for cell; the paint plan
// derives its raster scale from that band and this width, so a width of half
// the device pixels paints the page at 2x. The width rounds up so the painted
// band is never narrower than the raster (excess is cropped, never stretched).
func documentWidth(m *nthtml.Model, scale float64) int {
	cols := m.Bounds().Dx()
	w := cols * 10
	if m.Picture().Mode() == ntpicture.PictureKitty {
		cw, _ := m.Picture().CellPixelSize()
		if cw > 0 {
			w = cols * cw
		}
	}
	if scale > 1 {
		w = int(math.Ceil(float64(w) / scale))
	}
	return max(document.MinWidth, min(document.MaxWidth, w))
}

// documentWidth is the layout width for the current viewer and device scale.
func (m *Model) documentWidth() int { return documentWidth(m.viewer, m.deviceScale()) }

// cssPixelsPerCell is the terminal row height, in device pixels, that the
// automatic device scale equates with the page's 16 px default text: the page
// is scaled so one line of its default text is about one terminal row tall,
// to a whole factor. A 1x terminal (cells up to 23 px tall) keeps 1:1; a HiDPI
// terminal with 24..39 px cells gets 2x, as a browser on a retina display.
const cssPixelsPerCell = 16

// deviceScale is the device pixel ratio pages are laid out for: --scale when
// set, else from the cell height the picture (or the probed terminal) reports,
// 1 in glyph fallback.
func (m *Model) deviceScale() float64 {
	if m.pageScale >= 1 {
		return m.pageScale
	}
	_, ch := m.cellSize()
	return float64(max(1, min(4, int(math.Round(float64(ch)/cssPixelsPerCell)))))
}
