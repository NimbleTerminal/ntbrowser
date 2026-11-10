// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"nimbleterminal.dev/ntbrowser/loop"
	"strings"
	"testing"
)

func TestAddressURL(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "file:///tmp/page.html", "https://user:pass@example.test", "https://", "hello world", "", strings.Repeat("x", 8193)} {
		if _, err := addressURL(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for raw, want := range map[string]string{"example.test/a?q=hello": "https://example.test/a?q=hello", "http://example.test:8080/": "http://example.test:8080/", " example.test ": "https://example.test"} {
		if got, err := addressURL(raw); err != nil || got != want {
			t.Fatalf("%q: %q %v", raw, got, err)
		}
	}
}

func TestAddressStartupPasteAndNavigationGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400}, dropSink{})
	go l.Run()
	m := New(l, testProfile(nil, false, NavOff), "")
	m.Init()
	if !m.addressOpen || m.navSeq != 0 {
		t.Fatal("empty launch navigated")
	}
	if m.address.KeyMap.Paste.Enabled() {
		t.Fatal("clipboard helper enabled")
	}
	m.Update(tea.PasteMsg{Content: "https://example.test/search?q=hello"})
	if m.address.Value() != "https://example.test/search?q=hello" {
		t.Fatal(m.address.Value())
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.navSeq != 0 || !strings.HasPrefix(m.status.text, "blocked:") {
		t.Fatal("address bypassed navigation gate", m.status.text)
	}
	m.nav = NavConfirm
	m.openAddress()
	m.address.SetValue("example.test")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pendingNav != "https://example.test" || m.navSeq != 0 {
		t.Fatal("confirmation bypassed", m.pendingNav)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.navSeq != 1 {
		t.Fatal("confirmation did not start navigation")
	}
	m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if !m.addressOpen {
		t.Fatal("Ctrl+L did not open address")
	}
	m.Update(tea.PasteMsg{Content: "\x1b]52;c;evil\x07"})
	if hasTerminalControls(m.address.Value()) {
		t.Fatal("unsanitized address")
	}
	m.address.SetValue("file:///tmp/secret")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.addressOpen || m.navSeq != 1 || !strings.Contains(m.View().Content, "without credentials") {
		t.Fatal("invalid address submitted")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.addressOpen {
		t.Fatal("escape did not dismiss")
	}
}

func TestGlynxBrowsingInput(t *testing.T) {
	for raw, want := range map[string]string{
		"google.com":                  "https://google.com",
		"hello world":                 "https://www.google.com/search?q=hello+world",
		"hello":                       "https://www.google.com/search?q=hello",
		"https://example.test/?q=a+b": "https://example.test/?q=a+b",
	} {
		if got, err := BrowsingURL(raw); err != nil || got != want {
			t.Fatal(raw, got, err)
		}
	}
	for _, raw := range []string{"javascript:alert(1)", "file:///etc/passwd", "https://user:pass@site.test", ""} {
		if _, err := BrowsingURL(raw); err == nil {
			t.Fatal("invalid scheme/address accepted", raw)
		}
	}
	m := newModel(t, NavFree)
	m.SetGlynx()
	m.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if !m.addressOpen || !strings.Contains(m.address.Placeholder, "search") {
		t.Fatal("g did not open browsing input")
	}
	m.address.SetValue("hello world")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.navSeq != 1 || m.addressOpen {
		t.Fatal("search did not navigate")
	}
	if !strings.Contains(m.View().Content, "glynx") {
		t.Fatal("wrong application identity")
	}
	// A typed g in a form must not become a browser command.
	m.mode = modeEdit
	m.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if m.addressOpen {
		t.Fatal("browse shortcut stole form input")
	}
}
