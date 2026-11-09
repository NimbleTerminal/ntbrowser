// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"image"

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
// this viewer. It uses terminal cell metrics rather than a resident band's
// raster dimensions, or ten pixels per column in glyph fallback, clamped to
// nthtml's width bounds. The viewer's columns are already bounded by the
// Kitty placeholder grid (pictureCols), so the layout width matches the
// band the transport can show cell for cell.
func documentWidth(m *nthtml.Model) int {
	cols := m.Bounds().Dx()
	w := cols * 10
	if m.Picture().Mode() == ntpicture.PictureKitty {
		cw, _ := m.Picture().CellPixelSize()
		if cw > 0 {
			w = cols * cw
		}
	}
	return max(document.MinWidth, min(document.MaxWidth, w))
}
