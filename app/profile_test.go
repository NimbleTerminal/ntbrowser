// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"strings"
	"testing"

	"nimbleterminal.dev/nthtml"

	"nimbleterminal.dev/ntbrowser/profile"
)

func TestNavigateOffRefusesLinkButLoadsStart(t *testing.T) {
	m := newModel(t, profile.NavOff)
	seq := m.navSeq
	if cmd := m.admit("https://page.test/other", false); cmd != nil || m.navSeq != seq || !strings.Contains(m.status, "navigation is off") {
		t.Fatalf("cmd %v seq %d→%d status %q", cmd != nil, seq, m.navSeq, m.status)
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
