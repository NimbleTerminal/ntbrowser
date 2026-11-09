// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"image"
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/ntpicture"
)

// The painted band is the resident raster the viewer scrolls within; the
// transport's Kitty raster, its retained copy and compressed frames all scale
// with it, and a frame in flight lives beside the next one. Bounding the band
// keeps that transient well under the heap budget: three screens of terminal
// pixels, so a Page Down stays inside the resident band, but never more than
// 12 Mpx (48 MiB of RGBA) and never less than the viewport itself, so a HiDPI
// full screen gets a band of little more than one screen. ntpicture's own
// image cap still applies above that.
const (
	bandScreens   = 3
	bandPixelCap  = 12 << 20
	bandPixelsMin = 1 // a viewport always fits its band
)

// rasterBudget is the Kitty raster pixel cap for a viewport of cols×rows cells
// at the given cell geometry; nthtml plans band rows from it.
func rasterBudget(cols, rows, cellW, cellH int) int {
	px := int64(max(1, cols)) * int64(max(1, cellW)) * int64(max(1, rows)) * int64(max(1, cellH))
	budget := min(px*bandScreens, int64(bandPixelCap))
	budget = max(budget, px*bandPixelsMin)
	return int(min(budget, int64(ntpicture.MaxImagePixels)))
}

// pictureCols is the number of terminal columns the page picture may span:
// every column in glyph fallback, and at most the Kitty placeholder grid when
// the picture is (or the probed terminal will be) Kitty, where a wider
// placement is refused by the transport and would leave the page blank. The
// columns beyond it stay empty; the status lines still use the full width.
func (m *Model) pictureCols() int {
	cols := max(1, m.cols)
	kitty := false
	if m.viewer != nil {
		kitty = m.viewer.Picture().Mode() == ntpicture.PictureKitty
	} else if m.session != nil {
		kitty = m.session.Snapshot().Kitty == ntpicture.KittyCapabilitySupported
	}
	if kitty {
		cols = min(cols, nthtml.MaxKittyPlaceholderCells())
	}
	return cols
}

// cellSize is the terminal cell geometry the next paint should assume: the
// viewer's picture once one exists, else the probed terminal session, so the
// first band is painted at Kitty resolution instead of glyph resolution and
// then upscaled tenfold by the transport.
func (m *Model) cellSize() (int, int) {
	if m.viewer != nil {
		if m.viewer.Picture().Mode() == ntpicture.PictureKitty {
			if cw, ch := m.viewer.Picture().CellPixelSize(); cw > 0 && ch > 0 {
				return cw, ch
			}
			return 10, 20
		}
		return 1, 2
	}
	if m.session != nil {
		if st := m.session.Snapshot(); st.Kitty == ntpicture.KittyCapabilitySupported && st.CellPixelWidth > 0 && st.CellPixelHeight > 0 {
			return st.CellPixelWidth, st.CellPixelHeight
		}
	}
	return 1, 2
}

func (m *Model) syncViewport() tea.Cmd {
	if m.loop == nil || m.closing {
		return nil
	}
	var cmd tea.Cmd
	v := nthtml.PaintViewport{Cols: m.pictureCols(), Rows: m.viewRows()}
	v.CellWidth, v.CellHeight = m.cellSize()
	v.MaxRasterPixels = rasterBudget(v.Cols, v.Rows, v.CellWidth, v.CellHeight)
	if m.viewer != nil && m.cssPainted {
		if m.viewer.Picture().MaxRasterPixels() != v.MaxRasterPixels {
			cmd = m.viewer.Picture().SetMaxRasterPixels(v.MaxRasterPixels)
		}
		v = m.viewer.PaintViewport()
	}
	if m.resetView {
		v.Origin = image.Point{}
		v.ZoomSteps = 0
	}
	m.loop.SetViewport(v)
	if m.viewer != nil {
		m.loop.SetScale(m.deviceScale())
		width := m.documentWidth()
		if width != m.layoutWidth {
			m.layoutWidth = width
			m.loop.Resize(width)
		}
	}
	return cmd
}

func (m *Model) installCSSFrame(v loop.FrameMsg) tea.Cmd {
	if v.Painted.Revision != v.Gen || v.Painted.Result != v.Result {
		m.status = "render: mismatched frame revision"
		return nil
	}
	var desired nthtml.PaintViewport
	keep := m.viewer != nil && m.cssPainted && !m.resetView
	if keep {
		desired = m.viewer.PaintViewport()
	}
	var init tea.Cmd
	if m.viewer == nil || !m.cssPainted {
		if m.viewer != nil {
			init = m.viewer.Close()
		}
		opts := viewerOptions
		opts.Session = m.session
		cw, ch := m.cellSize()
		opts.Picture.Render.MaxRasterPixels = rasterBudget(m.pictureCols(), m.viewRows(), cw, ch)
		m.viewer = nthtml.NewWithOptions(nil, opts)
		m.cssPainted = true
		m.viewerInitialized = false
		m.viewer.SetBounds(0, 0, m.pictureCols(), m.viewRows())
	}
	cmd, err := m.viewer.SetPaintedResult(*v.Painted, !m.resetView)
	if err != nil {
		m.status = clean("render: " + err.Error())
		return init
	}
	// Init is idempotent at the picture level and only needed for this model.
	if !m.viewerInitialized {
		m.viewerInitialized = true
		init = tea.Batch(init, m.viewer.Init())
	}
	var restore []tea.Cmd
	if keep {
		restore = append(restore, m.viewer.Zoom(desired.ZoomSteps-m.viewer.PaintViewport().ZoomSteps))
		for range 32 {
			before := m.viewer.PaintViewport().Origin
			if before == desired.Origin {
				break
			}
			restore = append(restore, m.viewer.Pan(desired.Origin.X-before.X, desired.Origin.Y-before.Y))
			if m.viewer.PaintViewport().Origin == before {
				break
			}
		}
	}
	scroll := m.resetView
	m.resetView = false
	return tea.Batch(init, cmd, tea.Batch(restore...), m.applyRing(scroll))
}

func (m *Model) frameInputReady() bool {
	if m.viewer == nil || m.chunkFailed || m.resetView {
		return false
	}
	if m.cssPainted && m.viewer.PaintedRevision() != m.gen {
		return false
	}
	return m.viewer.InputReady() && m.loop.FrameCurrent(m.gen)
}

func (m *Model) cssFragment(href string) (tea.Cmd, bool) {
	if !m.cssPainted || m.viewer == nil || m.current == nil || !strings.Contains(href, "#") {
		return nil, false
	}
	u, err := url.Parse(href)
	if err != nil {
		return nil, false
	}
	current := *m.current
	current.Fragment, current.RawFragment = "", ""
	fragment := "#" + u.EscapedFragment()
	u.Fragment, u.RawFragment = "", ""
	if u.String() != current.String() {
		return nil, false
	}
	cmd, _ := m.viewer.NavigateFragment(fragment)
	return cmd, true
}
