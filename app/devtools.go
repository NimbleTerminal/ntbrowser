// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/ntbrowser/devtools"
)

type debugReply struct {
	serial   uint64
	response devtools.Response
}
type debugPanel struct {
	eventsCursor, eventsNext uint64
	session                  *devtools.Session
	open                     bool
	tab, scroll, frame       int
	serial                   uint64
	frames                   []devtools.Frame
	lines                    []string
	node                     uint64
	offset                   int
	dom                      *devtools.DOM
}

// SetDevTools attaches a host session; the frozen profile is checked separately.
func (m *Model) SetDevTools(s *devtools.Session) {
	if m.profile.DevTools {
		m.debug.session = s
	}
}
func (m *Model) debugRows() int {
	if m.debug.open {
		return min(12, max(0, m.rows-3), max(3, m.rows/2))
	}
	return 0
}
func (m *Model) debugRequest(method string) tea.Cmd {
	if m.debug.session == nil || !m.profile.DevTools {
		return nil
	}
	m.debug.serial++
	serial := m.debug.serial
	r := devtools.Request{Version: devtools.Version, Session: m.debug.session.ID(), Method: method}
	if method == "events" {
		r.After = m.debug.eventsCursor
	}
	if method == "dom" {
		r.Limit = 9
		if len(m.debug.frames) == 0 {
			return nil
		}
		f := m.debug.frames[m.debug.frame%len(m.debug.frames)]
		r.Frame, r.Revision, r.Node, r.Offset = f.ID, f.Revision, m.debug.node, m.debug.offset
	}
	s := m.debug.session
	return func() tea.Msg { return debugReply{serial, s.Do(context.Background(), r)} }
}
func (m *Model) debugRefresh() tea.Cmd {
	m.debug.scroll = 0
	switch m.debug.tab {
	case 1:
		return m.debugRequest("dom")
	case 2:
		return m.debugRequest("events")
	default:
		return m.debugRequest("snapshot")
	}
}
func (m *Model) debugUpdate(msg tea.Msg) (bool, tea.Cmd) {
	if reply, ok := msg.(debugReply); ok {
		if !m.debug.open || reply.serial != m.debug.serial {
			return true, nil
		}
		r := reply.response
		if r.Error != "" {
			m.debug.lines = []string{"Inspection: " + r.Error + " (refresh overview after navigation)"}
			return true, nil
		}
		m.debug.lines = nil
		if r.Snapshot != nil {
			m.debug.frames = r.Snapshot.Frames
			m.debug.frame = 0
			m.debug.node = 0
			m.debug.offset = 0
			for _, f := range r.Snapshot.Frames {
				m.debug.lines = append(m.debug.lines, fmt.Sprintf("frame %d · document %d · %s", f.ID, f.Revision, f.URL),
					fmt.Sprintf("nodes %d · DOM bytes %d · scripts %d · timers %d · RAF %d · pending scripts %d · disabled %t", f.Nodes, f.DOMBytes, f.Scripts, f.Timers, f.AnimationCallbacks, f.PendingScripts, f.Disabled))
			}
			m.debug.lines = append(m.debug.lines, strings.Split(r.Snapshot.Policy, "\n")...)
		}
		if r.DOM != nil {
			m.debug.dom = r.DOM
			m.debug.lines = append(m.debug.lines, fmt.Sprintf("frame %d · node %d <%s> · %d children", r.DOM.Frame, r.DOM.Node.ID, r.DOM.Node.Tag, r.DOM.Node.Children))
			for i, n := range r.DOM.Children {
				m.debug.lines = append(m.debug.lines, fmt.Sprintf("%d. #%d <%s> %s", i+1, n.ID, n.Tag, n.Text))
				for _, key := range []string{"id", "class", "type", "name", "role", "title", "alt", "href", "src", "style"} {
					if value, ok := n.Attributes[key]; ok {
						m.debug.lines = append(m.debug.lines, "    "+key+"="+value)
					}
				}
			}
			m.debug.lines = append(m.debug.lines, "1–9 inspect child · Backspace root · n next page · f next frame")
		}
		if r.Events != nil {
			m.debug.eventsNext = r.Events.Next
			m.debug.lines = append(m.debug.lines, "n next events · r refresh this page")
			if r.Events.Dropped > 0 {
				m.debug.lines = append(m.debug.lines, fmt.Sprintf("%d events dropped by size/encoding limits", r.Events.Dropped))
			}
			if r.Events.Lost > 0 {
				m.debug.lines = append(m.debug.lines, fmt.Sprintf("%d older events overwritten", r.Events.Lost))
			}
			for _, e := range r.Events.Items {
				m.debug.lines = append(m.debug.lines, fmt.Sprintf("#%d %s %s", e.Sequence, e.Kind, e.Data))
			}
			if len(m.debug.lines) == 0 {
				m.debug.lines = []string{"No events captured yet."}
			}
		}
		return true, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if ok && key.Code == tea.KeyF12 {
		if !m.profile.DevTools || m.debug.session == nil {
			m.status = "Devtools disabled; launch with --devtools"
			return true, nil
		}
		before := m.panelRows()
		m.debug.open = !m.debug.open
		m.debug.serial++
		m.pressed = false
		if !m.debug.open {
			return true, m.resizeIfPanel(before)
		}
		m.debug.tab = 0
		m.debug.lines = []string{"Loading inspection…"}
		return true, tea.Batch(m.leaveForms(), m.resizeIfPanel(before), m.debugRequest("snapshot"))
	}
	if !m.debug.open {
		return false, nil
	}
	if !ok {
		switch msg.(type) {
		case tea.PasteMsg, tea.MouseMsg, tea.KeyReleaseMsg:
			return true, nil
		}
		return false, nil
	}
	switch key.String() {
	case "ctrl+c":
		return true, m.quit()
	case "esc":
		before := m.panelRows()
		m.debug.open = false
		m.debug.serial++
		return true, m.resizeIfPanel(before)
	case "tab":
		m.debug.tab = (m.debug.tab + 1) % 3
		m.debug.node = 0
		m.debug.offset = 0
		m.debug.dom = nil
		return true, m.debugRefresh()
	case "r":
		return true, m.debugRefresh()
	case "up":
		m.debug.scroll = max(0, m.debug.scroll-1)
	case "down":
		m.debug.scroll = min(max(0, len(m.debug.lines)-1), m.debug.scroll+1)
	case "pgdown":
		m.debug.scroll = min(max(0, len(m.debug.lines)-1), m.debug.scroll+max(1, m.debugRows()-2))
	case "pgup":
		m.debug.scroll = max(0, m.debug.scroll-max(1, m.debugRows()-2))
	case "backspace":
		m.debug.node = 0
		m.debug.offset = 0
		return true, m.debugRefresh()
	case "n":
		if m.debug.tab == 2 {
			m.debug.eventsCursor = m.debug.eventsNext
			return true, m.debugRefresh()
		}
		if m.debug.tab == 1 && m.debug.dom != nil && m.debug.dom.Next >= 0 {
			m.debug.offset = m.debug.dom.Next
			return true, m.debugRefresh()
		}
	case "f":
		if m.debug.tab == 1 && len(m.debug.frames) > 0 {
			m.debug.frame = (m.debug.frame + 1) % len(m.debug.frames)
			m.debug.node = 0
			m.debug.offset = 0
			return true, m.debugRefresh()
		}
	default:
		if m.debug.tab == 1 && m.debug.dom != nil {
			k := key.String()
			if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
				i := int(k[0] - '1')
				if i < len(m.debug.dom.Children) {
					m.debug.node = m.debug.dom.Children[i].ID
					m.debug.offset = 0
					return true, m.debugRefresh()
				}
			}
		}
	}
	return true, nil
}
func (m *Model) debugView() string {
	rows := m.debugRows()
	if rows == 0 {
		return ""
	}
	names := []string{"Overview / permissions", "Elements", "Console / network"}
	lines := []string{fmt.Sprintf("Devtools · %s · Tab switch · r refresh · F12/Esc close", names[m.debug.tab])}
	for i := 0; i < rows-1; i++ {
		idx := m.debug.scroll + i
		line := ""
		if idx < len(m.debug.lines) {
			line = m.debug.lines[idx]
		}
		lines = append(lines, ansi.Truncate(strings.ReplaceAll(clean(line), "\n", " "), max(1, m.cols), "…"))
	}
	lines[0] = ansi.Truncate(lines[0], max(1, m.cols), "…")
	return strings.Join(lines, "\n")
}
