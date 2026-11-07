// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/devtools"
)

func TestDevToolsPanelUsesGatedAPIAndSanitizes(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(tea.KeyPressMsg{Code: tea.KeyF12})
	if m.debug.open {
		t.Fatal("panel bypassed profile gate")
	}
	m.profile.DevTools = true
	calls := 0
	s := devtools.New(true, func(_ context.Context, r devtools.Request) (devtools.Response, error) {
		calls++
		if r.Method == "snapshot" {
			return devtools.Response{Snapshot: &devtools.Snapshot{Frames: []devtools.Frame{{ID: 0, Revision: 7}}, Policy: "policy \x1b]52;c;EVIL\x07"}}, nil
		}
		if r.Revision != 7 || r.Limit != 9 {
			t.Fatal("panel lost revision or page bound", r)
		}
		return devtools.Response{DOM: &devtools.DOM{Revision: 7, Node: devtools.Node{ID: 1, Tag: "document"}, Children: []devtools.Node{{ID: 2, Tag: "#text", Text: "\x1b]52;c;EVIL\x07"}}, Next: -1}}, nil
	})
	m.SetDevTools(s)
	before := m.viewRows()
	m.Update(tea.KeyPressMsg{Code: tea.KeyF12})
	if !m.debug.open || m.viewRows() >= before {
		t.Fatal("panel did not reserve viewer space")
	}
	m.Update(m.debugRequest("snapshot")())
	if hasTerminalControls(m.View().Content) {
		t.Fatal("unsanitized policy")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(cmd())
	if hasTerminalControls(m.View().Content) {
		t.Fatal("unsanitized DOM")
	}
	if calls != 2 || !strings.Contains(m.View().Content, "Devtools") {
		t.Fatal(calls)
	}
	// Keys belong to the inspector, never to the page underneath it.
	m.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if m.addressOpen {
		t.Fatal("panel key reached browser")
	}
	old := debugReply{m.debug.serial, devtools.Response{Error: "OLD"}}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(old)
	if m.debug.open || m.viewRows() != before {
		t.Fatal("panel did not restore viewer bounds")
	}
}
