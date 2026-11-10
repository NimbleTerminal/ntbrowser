// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"strings"
	"testing"

	"nimbleterminal.dev/nthtml"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
)

func TestNavigateOffRefusesLinkButLoadsStart(t *testing.T) {
	m := newModel(t, profile.NavOff)
	m.start = "https://page.test/"
	seq := m.navSeq
	if cmd := m.admit("https://page.test/other", false); cmd != nil || m.navSeq != seq || !strings.Contains(m.status.text, "navigation is off") {
		t.Fatalf("cmd %v seq %d→%d status %q", cmd != nil, seq, m.navSeq, m.status.text)
	}
	if m.Init() == nil || m.navSeq != seq+1 {
		t.Fatal("the start URL must still load")
	}
}

func TestClipboardOffDropsCopy(t *testing.T) {
	m := newModel(t, NavFree)
	m.profile.Clipboard = profile.ClipboardOff
	if _, cmd := m.Update(nthtml.CopyMsg{Text: "x"}); cmd != nil {
		t.Fatal("copied")
	}
	m.profile.Clipboard = profile.ClipboardCopy
	if _, cmd := m.Update(nthtml.CopyMsg{Text: "x"}); cmd == nil {
		t.Fatal("copy dropped")
	}
}

func TestHelpLineNamesProfile(t *testing.T) {
	m := newModel(t, NavFree)
	if !strings.Contains(statusLines(m), "profile test") {
		t.Fatalf("%q", statusLines(m))
	}
}

func TestHelpSaysWhenNotEnforced(t *testing.T) {
	m := newModel(t, NavFree)
	if strings.Contains(statusLines(m), "not OS-enforced") {
		t.Fatal(statusLines(m))
	}
	m.SetEnforced(false)
	if !strings.Contains(statusLines(m), "not OS-enforced") {
		t.Fatal(statusLines(m))
	}
}

// Anchor URL properties produce data. Every final reader-activated
// destination still enters the same profile gate, regardless of its source.
func TestAnchorDestinationProfilePerimeter(t *testing.T) {
	for _, tc := range []struct {
		mode            NavMode
		href            string
		allowed, prompt bool
	}{
		{profile.NavOff, "https://page.test/next", false, false},
		{NavAllowlist, "https://other.test/next", false, false},
		{NavAllowlist, "https://cdn.test/next", true, false},
		{NavConfirm, "https://other.test/next", false, true},
		{NavConfirm, "https://page.test/next", true, false},
		{NavFree, "https://other.test/next", true, false},
	} {
		t.Run(string(tc.mode)+tc.href, func(t *testing.T) {
			m := newModel(t, tc.mode)
			_, cmd := m.Update(loop.NavigateMsg{Href: tc.href})
			if (cmd != nil) != tc.allowed || (m.pendingNav != "") != tc.prompt {
				t.Fatalf("cmd=%v prompt=%q status=%q", cmd != nil, m.pendingNav, m.status.text)
			}
		})
	}
}
