// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/origin"
	"nimbleterminal.dev/ntbrowser/profile"
)

func (m *Model) scriptPermission(message loop.ScriptPermissionMsg) {
	if message.Expired {
		for i, pending := range m.scriptPermissions {
			if pending.ID == message.ID {
				m.scriptPermissions = append(m.scriptPermissions[:i], m.scriptPermissions[i+1:]...)
				break
			}
		}
		return
	}
	if m.current == nil || message.Source != m.current.String() || message.Navigation != m.documentNav || m.navSeq != m.documentSeq || !m.profile.ScriptPrompts || m.profile.Scripts != profile.ScriptsExternal || m.pendingNav != "" || m.mode != modeBrowse || m.overlay() && len(m.scriptPermissions) == 0 || (m.viewer != nil && m.viewer.SearchInputActive()) || len(m.scriptPermissions) >= 8 {
		m.loop.DecideScriptPermission(message.ID, message.Navigation, "deny")
		return
	}
	for _, pending := range m.scriptPermissions {
		if pending.ID == message.ID {
			return
		}
	}
	m.scriptPermissions = append(m.scriptPermissions, message)
}
func (m *Model) scriptPermissionStatus() string {
	if len(m.scriptPermissions) == 0 {
		return ""
	}
	request := m.scriptPermissions[0]
	source, _ := origin.Of(request.Source)
	return clean(fmt.Sprintf("Script request: %s → %s", source, request.Destination))
}
func (m *Model) scriptPermissionKey(key string) bool {
	if len(m.scriptPermissions) == 0 {
		return false
	}
	decision := ""
	switch key {
	case "alt+o":
		decision = "once"
	case "alt+s":
		decision = "session"
	case "esc":
		decision = "deny"
	case "q", "ctrl+c":
		return false
	default:
		return true
	}
	if decision != "deny" && ansi.StringWidth(m.scriptPermissionStatus()) > m.cols {
		return true
	}
	request := m.scriptPermissions[0]
	m.scriptPermissions = m.scriptPermissions[1:]
	if m.current == nil || request.Source != m.current.String() || request.Navigation != m.documentNav || m.navSeq != m.documentSeq {
		decision = "deny"
	}
	m.loop.DecideScriptPermission(request.ID, request.Navigation, decision)
	m.setStatus(statusPlain, "script permission: "+decision)
	return true
}
