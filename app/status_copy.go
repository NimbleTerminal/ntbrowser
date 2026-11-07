// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/profile"
)

// Copy the original message rather than its truncated, colored presentation.
// Never copy form-editor contents or read the system clipboard.
func (m *Model) copyStatus() tea.Cmd {
	if m.profile.Clipboard != profile.ClipboardCopy {
		return nil
	}
	text := m.status
	if m.addressOpen && m.addressError != "" {
		text = m.addressError
	}
	if text == "" {
		return nil
	}
	return tea.SetClipboard(clean(text))
}
