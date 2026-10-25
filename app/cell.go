package app

import (
	"image"
	"math"

	"nimbleterminal.dev/nthtml"
)

// cellToDocument maps a terminal cell to the document-pixel area it shows,
// using only what nthtml v0.1.0 exports: the widget bounds, the picture's
// raster geometry and the current source crop. It is the same arithmetic as
// nthtml's unexported rasterCellSource (model_mouse.go) and cell_test.go
// checks it against Model.LinkAtCell, which uses that path. It reports false
// in TTY layout mode, where cells index TTY lines rather than document
// pixels, and before the viewer has a Result.
//
// If nthtml later exports this (design §11), delete this file and call it.
func cellToDocument(m *nthtml.Model, p image.Point) (image.Rectangle, bool) {
	if m == nil || m.LayoutMode() != nthtml.LayoutTypeset || m.Result() == nil {
		return image.Rectangle{}, false
	}
	b := m.Bounds()
	p = p.Sub(b.Min)
	cells := b.Size()
	if p.X < 0 || p.Y < 0 || p.X >= cells.X || p.Y >= cells.Y {
		return image.Rectangle{}, false
	}
	pixels := m.Picture().RasterGeometry().Pixels
	crop := m.Picture().Crop()
	if crop.Empty() {
		crop = m.Result().Bounds()
	}
	if cells.X <= 0 || cells.Y <= 0 || pixels.X <= 0 || pixels.Y <= 0 || crop.Empty() {
		return image.Rectangle{}, false
	}
	// Letterbox the crop into the raster target, then project the cell back.
	tw, th := pixels.X, pixels.Y
	sw, sh := crop.Dx(), crop.Dy()
	iw, ih := tw, th
	if int64(sw)*int64(th) >= int64(sh)*int64(tw) {
		ih = max(1, int(int64(sh)*int64(tw)/int64(sw)))
	} else {
		iw = max(1, int(int64(sw)*int64(th)/int64(sh)))
	}
	ox, oy := (tw-iw)/2, (th-ih)/2
	left := math.Max(float64(ox), float64(p.X)*float64(tw)/float64(cells.X))
	top := math.Max(float64(oy), float64(p.Y)*float64(th)/float64(cells.Y))
	right := math.Min(float64(ox+iw), float64(p.X+1)*float64(tw)/float64(cells.X))
	bottom := math.Min(float64(oy+ih), float64(p.Y+1)*float64(th)/float64(cells.Y))
	if right <= left || bottom <= top {
		return image.Rectangle{}, false
	}
	area := image.Rect(
		crop.Min.X+int(math.Floor((left-float64(ox))*float64(sw)/float64(iw))),
		crop.Min.Y+int(math.Floor((top-float64(oy))*float64(sh)/float64(ih))),
		crop.Min.X+int(math.Ceil((right-float64(ox))*float64(sw)/float64(iw))),
		crop.Min.Y+int(math.Ceil((bottom-float64(oy))*float64(sh)/float64(ih))),
	).Intersect(crop)
	return area, !area.Empty()
}

// documentWidth is the CSS-pixel width the loop should lay pages out at for
// this viewer. It mirrors nthtml's own ReflowTypeset choice (model.go
// SetSize): the raster target width, or ten pixels per column when the
// picture is in glyph fallback (one pixel per cell) or has no geometry yet,
// clamped to [32, 4096].
func documentWidth(m *nthtml.Model) int {
	cols := m.Bounds().Dx()
	w := m.Picture().RasterGeometry().Pixels.X
	if w <= 0 || w == cols {
		w = cols * 10
	}
	return max(32, min(4096, w))
}
