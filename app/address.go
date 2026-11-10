// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func addressURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 8192 || strings.ContainsFunc(raw, unicode.IsSpace) {
		return "", fmt.Errorf("enter an HTTP(S) address without spaces")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("enter an HTTP(S) address without credentials")
	}
	return u.String(), nil
}

func (m *Model) openAddress() tea.Cmd {
	leave := m.leaveForms()
	m.address = textinput.New()
	m.address.Prompt = "Address: "
	m.address.Placeholder = "https://example.com"
	if m.glynx {
		m.address.Prompt = "Go: "
		m.address.Placeholder = "URL or Google search"
	}
	m.address.CharLimit = 8192
	m.address.SetWidth(max(1, m.cols-10))
	m.address.KeyMap.Paste.SetEnabled(false) // terminal paste only; never execute a clipboard helper
	if m.current != nil {
		m.address.SetValue(clean(m.current.String()))
	}
	m.address.CursorEnd()
	m.addressError = ""
	m.addressOpen = true
	m.pressed = false
	return tea.Batch(leave, m.address.Focus())
}

func (m *Model) addressUpdate(msg tea.Msg) (bool, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		switch v.String() {
		case "ctrl+c":
			return true, m.quit()
		case "esc":
			m.addressOpen = false
			m.address.Blur()
			return true, nil
		case "enter":
			href, err := addressURL(m.address.Value())
			if m.glynx {
				href, err = BrowsingURL(m.address.Value())
			}
			if err != nil {
				m.addressError = err.Error()
				m.setStatus(statusPlain, "address: "+err.Error())
				return true, nil
			}
			m.addressOpen = false
			m.address.Blur()
			return true, m.admit(href, false)
		}
	case tea.PasteMsg:
		msg = tea.PasteMsg{Content: strings.ReplaceAll(clean(v.Content), "\n", " ")}
	case cursor.BlinkMsg:
		var cmd tea.Cmd
		m.address, cmd = m.address.Update(msg)
		return true, cmd
	case tea.KeyReleaseMsg:
		return true, nil
	case tea.MouseMsg:
		return true, nil
	case tea.WindowSizeMsg:
		m.address.SetWidth(max(1, v.Width-10))
		return false, nil
	default:
		return false, nil
	}
	m.addressError = ""
	var cmd tea.Cmd
	m.address, cmd = m.address.Update(msg)
	m.address.SetValue(strings.ReplaceAll(clean(m.address.Value()), "\n", " "))
	return true, cmd
}

// BrowsingURL accepts an address or a search query from the host's browsing UI.
// Explicit schemes retain address validation; unsupported schemes never execute.
func BrowsingURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 8192 {
		return "", fmt.Errorf("enter a URL or search query (at most 8192 bytes)")
	}
	if !strings.Contains(raw, ":") && (strings.ContainsFunc(raw, unicode.IsSpace) || !strings.ContainsAny(raw, "./")) {
		return "https://www.google.com/search?q=" + url.QueryEscape(raw), nil
	}
	return addressURL(raw)
}
