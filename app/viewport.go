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
)

func (m *Model) syncViewport() {
	if m.loop == nil || m.closing {
		return
	}
	v := nthtml.PaintViewport{Cols: m.cols, Rows: m.viewRows()}
	if m.viewer != nil && m.cssPainted {
		v = m.viewer.PaintViewport()
	}
	if m.resetView {
		v.Origin = image.Point{}
		v.ZoomSteps = 0
	}
	m.loop.SetViewport(v)
	if m.viewer != nil {
		width := documentWidth(m.viewer)
		if width != m.layoutWidth {
			m.layoutWidth = width
			m.loop.Resize(width)
		}
	}
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
		m.viewer = nthtml.NewWithOptions(nil, viewerOptions)
		m.cssPainted = true
		m.viewerInitialized = false
		m.viewer.SetBounds(0, 0, m.cols, m.viewRows())
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
