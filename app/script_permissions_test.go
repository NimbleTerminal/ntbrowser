// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	"context"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
	"strings"
	"testing"
)

func permissionModel(t *testing.T) *Model {
	m := locationModel(t, profile.ScriptNavConfirm)
	m.profile.ScriptPrompts = true
	m.profile.Scripts = profile.ScriptsExternal
	m.cols = 120
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.loop = loop.New(ctx, loop.Config{Profile: m.profile}, dropSink{})
	go m.loop.Run()
	return m
}
func TestScriptPermissionPromptIdentityAndReaderControls(t *testing.T) {
	m := permissionModel(t)
	request := loop.ScriptPermissionMsg{ID: 1, Navigation: m.documentNav, Source: m.current.String(), Destination: "https://cdn.test", URL: "https://cdn.test/a.js"}
	stale := request
	stale.Navigation++
	m.scriptPermission(stale)
	if len(m.scriptPermissions) != 0 {
		t.Fatal("stale prompt displayed")
	}
	m.scriptPermission(request)
	if len(m.scriptPermissions) != 1 || !m.overlay() || !strings.Contains(m.scriptPermissionStatus(), "https://cdn.test") {
		t.Fatal("prompt not displayed")
	}
	m.scriptPermissionKey("s")
	if len(m.scriptPermissions) != 1 {
		t.Fatal("ordinary typing approved a prompt")
	}
	m.cols = 10
	m.scriptPermissionKey("alt+s")
	if len(m.scriptPermissions) != 1 {
		t.Fatal("approved truncated origins")
	}
	m.cols = 120
	m.scriptPermissionKey("alt+o")
	if len(m.scriptPermissions) != 0 {
		t.Fatal("one-request choice not consumed")
	}
	m.scriptPermission(request)
	m.scriptNavigate(locationRequest(m, "https://other.test/", "assign"))
	if m.pendingNav != "" {
		t.Fatal("script replaced permission prompt")
	}
	expired := request
	expired.Expired = true
	m.scriptPermission(expired)
	if len(m.scriptPermissions) != 0 {
		t.Fatal("expired prompt retained")
	}
}
func TestScriptPermissionDoesNotInterruptEditingOrNavigation(t *testing.T) {
	m := permissionModel(t)
	request := loop.ScriptPermissionMsg{ID: 1, Navigation: m.documentNav, Source: m.current.String(), Destination: "https://cdn.test"}
	m.mode = modeEdit
	m.scriptPermission(request)
	if len(m.scriptPermissions) != 0 {
		t.Fatal("prompt interrupted editor")
	}
	m.mode = modeBrowse
	m.pendingNav = "https://reader.test"
	m.scriptPermission(request)
	if len(m.scriptPermissions) != 0 || m.pendingNav != "https://reader.test" {
		t.Fatal("prompt replaced navigation confirmation")
	}
	m.pendingNav = ""
	m.navSeq++
	m.scriptPermission(request)
	if len(m.scriptPermissions) != 0 {
		t.Fatal("prompt arrived during navigation")
	}
}
