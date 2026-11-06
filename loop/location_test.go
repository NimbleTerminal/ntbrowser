// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/profile"
	"testing"
)

func TestLocationRequestsCoalesceAndStayBoundToDocument(t *testing.T) {
	p, _ := profile.Builtin("browse")
	p.ScriptNavigation = profile.ScriptNavOrigin
	p.ScriptNavigationLimit = 1
	_, sink := profileLoop(t, p, `<script>location.assign("/first");location.replace("/last");setTimeout(()=>{try{location.reload()}catch(e){console.log(e.name)}},20)</script>`)
	msg := waitFor(t, sink, func(m tea.Msg) bool { _, ok := m.(ScriptNavigateMsg); return ok }).(ScriptNavigateMsg)
	if msg.Href != "https://example.test/last" || msg.Mode != "replace" || msg.Source != "https://example.test/" || msg.Navigation != 1 {
		t.Fatal(msg)
	}
	waitLog(t, sink, "SecurityError")
}
func TestLocationGateClosedInLoop(t *testing.T) {
	p, _ := profile.Builtin("browse")
	_, sink := profileLoop(t, p, `<script>try{location.assign("/next")}catch(e){console.log(e.name)}</script>`)
	waitLog(t, sink, "SecurityError")
}
