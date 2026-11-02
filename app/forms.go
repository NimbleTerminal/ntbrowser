// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/loop"
)

// inputMode routes keys: browse (scroll, links, focus), edit (a text field's
// editor), choose (a select's chooser). Forms design decisions 4–9.
type inputMode int

const (
	modeBrowse inputMode = iota
	modeEdit
	modeChoose
)

// textareaRows is the editor panel a textarea opens above the help line.
const textareaRows = 6

func isTextKind(kind string) bool { return kind == "text" || kind == "password" || kind == "textarea" }

// panelRows is how many rows the editor panel takes from the viewer.
func (m *Model) panelRows() int {
	if m.mode == modeEdit && m.focus.Control.Kind == "textarea" {
		return textareaRows
	}
	return 0
}

// label is the focused element's name for the status line (page text).
func (m *Model) label() string {
	if l := m.focus.Control.Label; l != "" {
		return l
	}
	if k := m.focus.Control.Kind; k != "" {
		return k
	}
	return "element"
}

// onFocus handles a focus report from the loop. Only reader actions start
// text entry; script focus moves the ring and the status line only.
func (m *Model) onFocus(f loop.FocusMsg) tea.Cmd {
	wasPanel := m.panelRows()
	editing := m.mode != modeBrowse
	m.focus = f
	if f.Source == "" {
		m.mode = modeBrowse
		if strings.HasPrefix(m.status, "focus: ") {
			m.status = ""
		}
		return tea.Batch(m.applyRing(false), m.resizeIfPanel(wasPanel))
	}
	cmds := []tea.Cmd{m.applyRing(true)}
	m.status = clean("focus: " + m.label())
	switch {
	case f.User && isTextKind(f.Control.Kind):
		cmds = append(cmds, m.startEdit())
	case editing:
		m.mode = modeBrowse // script moved focus away from the field being edited
	}
	cmds = append(cmds, m.resizeIfPanel(wasPanel))
	return tea.Batch(cmds...)
}

// applyRing draws the focus ring on the focused element. scroll brings it
// into view (a focus move); without it the view stays put, which is how the
// ring is re-applied after a frame, since SetResult clears it. An element the
// frame no longer draws simply has no ring.
func (m *Model) applyRing(scroll bool) tea.Cmd {
	if m.viewer == nil {
		return nil
	}
	var cmd tea.Cmd
	var ok bool
	if scroll {
		cmd, ok = m.viewer.FocusSource(m.focus.Source)
	} else {
		cmd, ok = m.viewer.ShowFocusSource(m.focus.Source)
	}
	m.ring = ""
	if ok {
		m.ring = m.focus.Source
	}
	return cmd
}

// resizeIfPanel re-bounds the viewer when the textarea panel opened or closed.
func (m *Model) resizeIfPanel(before int) tea.Cmd {
	if m.viewer == nil || before == m.panelRows() {
		return nil
	}
	return m.viewer.SetBounds(0, 0, m.cols, m.viewRows())
}

// startEdit opens the editor on the focused field, seeded with its current
// value. A password is seeded too but only ever drawn masked.
func (m *Model) startEdit() tea.Cmd {
	c := m.focus.Control
	m.mode = modeEdit
	seed := clean(c.Value)
	if c.Kind == "textarea" {
		seed = cleanLines(c.Value)
		m.area = textarea.New()
		m.area.MaxHeight = 0 // no 99-line cap; the view stays textareaRows high
		m.area.ShowLineNumbers = false
		m.area.Prompt = ""
		m.area.CharLimit = c.MaxLength
		m.area.SetWidth(max(10, m.cols))
		m.area.SetHeight(textareaRows)
		m.area.SetValue(seed)
		m.area.KeyMap.Paste.SetEnabled(false) // ctrl+v would exec a clipboard tool; terminal paste arrives as tea.PasteMsg
		if c.ReadOnly {
			return nil
		}
		return m.area.Focus()
	}
	m.input = textinput.New()
	m.input.Prompt = "✎ " + clean(m.label()) + ": "
	m.input.CharLimit = c.MaxLength
	if c.Kind == "password" {
		m.input.EchoMode = textinput.EchoPassword
		m.input.EchoCharacter = '•'
	}
	m.input.SetWidth(max(10, m.cols-len([]rune(m.input.Prompt))-1))
	m.input.SetValue(seed)
	m.input.CursorEnd()
	m.input.KeyMap.Paste.SetEnabled(false) // as above: no clipboard tool, only bracketed paste
	if c.ReadOnly {
		return nil
	}
	return m.input.Focus()
}

func (m *Model) editorValue() string {
	if m.focus.Control.Kind == "textarea" {
		return m.area.Value()
	}
	return m.input.Value()
}

// commit sends the editor's value as final and leaves edit mode.
func (m *Model) commit() tea.Cmd {
	before := m.panelRows()
	if !m.focus.Control.ReadOnly {
		m.focus.Control.Value = m.editorValue() // the seed if editing resumes
		m.loop.SetValue(m.focus.Source, m.focus.Control.Value, true)
	}
	m.mode = modeBrowse
	m.status = clean("focus: " + m.label())
	return m.resizeIfPanel(before)
}

// onValue follows a script write to the field being edited.
func (m *Model) onValue(v loop.ValueMsg) {
	if v.Source != m.focus.Source {
		return
	}
	m.focus.Control.Value = v.Value
	if m.mode != modeEdit || v.Value == m.editorValue() {
		return // nothing to show, or a rewrite of what is shown: keep the cursor
	}
	if m.focus.Control.Kind == "textarea" {
		m.area.SetValue(cleanLines(v.Value))
		m.area.CursorEnd()
		return
	}
	m.input.SetValue(clean(v.Value))
	m.input.CursorEnd()
}

// cleanLines is clean for multi-line text: it keeps line breaks (and turns
// tabs into spaces) so a textarea's lines survive the editor.
func cleanLines(s string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\t", "    "), "\n")
	for i, l := range lines {
		lines[i] = clean(l)
	}
	return strings.Join(lines, "\n")
}

// leaveForms resets the input mode for a new page.
func (m *Model) leaveForms() tea.Cmd {
	before := m.panelRows()
	m.mode, m.focus = modeBrowse, loop.FocusMsg{}
	return m.resizeIfPanel(before)
}

// formKey routes a key by input mode; handled reports that the key is
// consumed. ctrl+c is never consumed, so it always quits.
func (m *Model) formKey(k tea.KeyPressMsg) (handled bool, cmd tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		return false, nil
	}
	switch m.mode {
	case modeEdit:
		switch key {
		case "tab", "shift+tab":
			cmd := m.commit()
			m.loop.FocusNext(key == "shift+tab")
			return true, cmd
		case "esc":
			return true, m.commit()
		case "enter":
			if m.focus.Control.Kind != "textarea" {
				source := m.focus.Source
				cmd := m.commit()
				m.loop.ImplicitSubmit(source)
				return true, cmd
			}
		}
		if m.focus.Control.ReadOnly {
			return true, nil
		}
		before := m.editorValue()
		if m.focus.Control.Kind == "textarea" {
			m.area, cmd = m.area.Update(k)
		} else {
			m.input, cmd = m.input.Update(k)
		}
		if after := m.editorValue(); after != before {
			m.loop.SetValue(m.focus.Source, after, false)
		}
		m.loop.Input(k) // the page's keydown copy; it cannot cancel text entry (decision 6)
		return true, cmd
	case modeChoose:
		c := m.focus.Control
		switch key {
		case "up", "k":
			m.choice = (m.choice - 1 + len(c.Options)) % len(c.Options)
		case "down", "j":
			m.choice = (m.choice + 1) % len(c.Options)
		case "space":
			if c.Multiple {
				m.loop.Choose(m.focus.Source, c.OptionIndex[m.choice], true)
				m.toggleSelected(m.choice)
			}
		case "enter":
			if !c.Multiple {
				m.loop.Choose(m.focus.Source, c.OptionIndex[m.choice], false)
				m.focus.Control.Selected = []int{m.choice}
			}
			m.mode = modeBrowse
		case "esc":
			m.mode = modeBrowse
		}
		return true, nil
	}
	// Browse mode: Tab, Shift+Tab and Enter belong to the page's focus, never
	// to the viewer's own link focus (decision 1).
	switch key {
	case "tab", "shift+tab":
		m.loop.FocusNext(key == "shift+tab")
		m.loop.Input(k)
		return true, nil
	case "enter":
		if m.focus.Source == "" {
			return true, nil
		}
		switch kind := m.focus.Control.Kind; {
		case isTextKind(kind):
			return true, m.startEditWithResize()
		case kind == "select":
			if len(m.focus.Control.Options) > 0 {
				m.mode = modeChoose
				m.choice = 0
				if sel := m.focus.Control.Selected; len(sel) > 0 {
					m.choice = sel[0]
				}
			}
		default:
			m.loop.Activate()
		}
		m.loop.Input(k)
		return true, nil
	case "space":
		switch m.focus.Control.Kind {
		case "checkbox", "radio", "button":
			m.loop.Activate()
			return true, nil
		}
	}
	return false, nil
}

func (m *Model) startEditWithResize() tea.Cmd {
	before := m.panelRows()
	cmd := m.startEdit()
	return tea.Batch(cmd, m.resizeIfPanel(before))
}

func (m *Model) toggleSelected(i int) {
	sel := m.focus.Control.Selected
	for j, s := range sel {
		if s == i {
			m.focus.Control.Selected = append(sel[:j:j], sel[j+1:]...)
			return
		}
	}
	m.focus.Control.Selected = append(sel, i)
}

// formStatus is what replaces the status line in edit and choose modes;
// everything page-controlled in it is cleaned.
func (m *Model) formStatus() (line string, ok bool) {
	switch m.mode {
	case modeEdit:
		if m.focus.Control.Kind == "textarea" {
			return m.area.View() + "\n" + clean("✎ "+m.label()), true
		}
		return m.input.View(), true
	case modeChoose:
		c := m.focus.Control
		mark := ""
		if c.Multiple {
			mark = "[ ] "
			for _, s := range c.Selected {
				if s == m.choice {
					mark = "[x] "
				}
			}
		}
		return clean(fmt.Sprintf("▾ %s: %s%s (%d/%d)", m.label(), mark, c.Options[m.choice], m.choice+1, len(c.Options))), true
	}
	return "", false
}

// helpLine is the bottom line's key summary for the current mode.
func (m *Model) helpLine() string {
	switch m.mode {
	case modeEdit:
		if m.focus.Control.Kind == "textarea" {
			return " editing · Tab next · Esc done · ^C quit"
		}
		return " editing · Enter submit · Tab next · Esc done · ^C quit"
	case modeChoose:
		if m.focus.Control.Multiple {
			return " ↑↓ choose · Space toggle · Enter done · Esc cancel"
		}
		return " ↑↓ choose · Enter set · Esc cancel"
	}
	return ""
}

// onPaste feeds a terminal (bracketed) paste to the editor in edit mode.
func (m *Model) onPaste(p tea.PasteMsg) (handled bool, cmd tea.Cmd) {
	if m.mode != modeEdit || m.focus.Control.ReadOnly || m.pendingNav != "" {
		return false, nil
	}
	before := m.editorValue()
	if m.focus.Control.Kind == "textarea" {
		m.area, cmd = m.area.Update(p)
	} else {
		m.input, cmd = m.input.Update(p)
	}
	if after := m.editorValue(); after != before {
		m.loop.SetValue(m.focus.Source, after, false)
	}
	return true, cmd
}
