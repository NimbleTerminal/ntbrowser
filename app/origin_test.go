// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"testing"

	"nimbleterminal.dev/ntbrowser/loop"
)

// Under navigate=confirm a link asks only when it leaves the origin, and an
// explicit default port names the same origin as an omitted one.
func TestConfirmTreatsDefaultPortAsSameOrigin(t *testing.T) {
	for href, prompt := range map[string]bool{
		"https://page.test/b":      false,
		"https://page.test:443/b":  false,
		"https://PAGE.test/b":      false,
		"https://page.test:8443/b": true,
		"http://page.test/b":       true,
		"https://other.test/b":     true,
	} {
		m := newModel(t, NavConfirm)
		m.Update(loop.NavigateMsg{Href: href})
		if got := m.pendingNav != ""; got != prompt {
			t.Errorf("%s: prompted=%v, want %v", href, got, prompt)
		}
	}
}
