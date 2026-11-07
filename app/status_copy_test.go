// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/profile"
)

func TestCopyFullStatusRespectsGateAndSanitization(t *testing.T) {
	m := newModel(t, NavFree)
	m.profile.Clipboard = profile.ClipboardCopy
	m.cols = 20
	message := "render: nthtml: resource limit exceeded: diagram count"
	m.status = message + "\x1b]52;c;evil\x07"
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if cmd == nil {
		t.Fatal("no clipboard command")
	}
	if got := cmd(); !reflect.DeepEqual(got, tea.SetClipboard(clean(m.status))()) {
		t.Fatal("copied clipped/styled/unsafe message", got)
	}
	if !strings.Contains(clean(m.status), message) {
		t.Fatal("message replaced after copying")
	}
	m.profile.Clipboard = profile.ClipboardOff
	if _, cmd = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}); cmd != nil {
		t.Fatal("clipboard gate bypassed")
	}
}
