// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml"
)

// chunkKey keeps the ordinary viewer's navigation within a chunk and crosses
// its boundary only when the reader scrolls again at the edge. Explicit jumps
// also let the reader move past an indivisible chunk that exceeded its budget.
func (m *Model) chunkKey(key string) bool {
	if m.chunk.Count == 0 || m.overlay() {
		return false
	}
	delta, end := 0, 0
	switch key {
	case "[":
		delta = -1
	case "]":
		delta = 1
	case "ctrl+home":
		end = -1
	case "ctrl+end":
		end = 1
	case "down", "j", "pgdown", "up", "k", "pgup":
		if m.chunkFailed {
			if key == "down" || key == "j" || key == "pgdown" {
				delta = 1
			} else {
				delta = -1
			}
			break
		}
		if m.viewer == nil || m.viewer.LayoutMode() != nthtml.LayoutTypeset {
			return false
		}
		crop, visible := m.viewer.DocumentViewport()
		if !visible || crop.Empty() {
			return false
		}
		if (key == "down" || key == "j" || key == "pgdown") && crop.Max.Y >= m.viewer.Result().Bounds().Max.Y {
			delta = 1
		}
		if (key == "up" || key == "k" || key == "pgup") && crop.Min.Y <= 0 {
			delta = -1
		}
	default:
		return false
	}
	if delta == 0 && end == 0 {
		return false
	}
	m.loop.MoveChunk(m.gen, delta, end)
	return true
}

func (m *Model) finishChunk() tea.Cmd {
	if m.chunk.Count == 0 || m.viewer == nil {
		return nil
	}
	if m.chunk.Anchor != "" {
		if strings.HasPrefix(m.chunk.Anchor, "#") {
			cmd, _ := m.viewer.NavigateFragment(m.chunk.Anchor)
			return cmd
		}
		cmd, _ := m.viewer.NavigateSource(m.chunk.Anchor)
		return cmd
	}
	if m.chunk.Bottom {
		return m.viewer.Pan(0, m.viewer.Result().Bounds().Dy())
	}
	return nil
}

// Same-document fragments stay inside this snapshot and do not fetch or
// restart scripts. External navigation still passes through admit's gates.
func (m *Model) chunkFragment(href string) bool {
	if m.chunk.Count == 0 || m.current == nil {
		return false
	}
	u, err := url.Parse(href)
	if err != nil || !strings.Contains(href, "#") {
		return false
	}
	current := *m.current
	current.Fragment, current.RawFragment = "", ""
	fragment := "#" + u.EscapedFragment()
	u.Fragment, u.RawFragment = "", ""
	if u.String() != current.String() {
		return false
	}
	m.loop.RevealChunk(m.gen, fragment)
	return true
}

// Chunk-local hits must describe the image actually presented, not a pending
// replacement. TTY has no asynchronous raster presentation.
func (m *Model) chunkMouseReady() bool {
	if m.chunkFailed || m.viewer == nil {
		return false
	}
	if m.viewer.LayoutMode() == nthtml.LayoutTTY {
		return true
	}
	p := m.viewer.Picture()
	status := p.Status()
	return p.Visible() && !p.Closed() && status.Err == nil && status.Presented > 0 && status.Presented == status.Generation
}
