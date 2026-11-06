// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import "strings"

// ScriptNavigateMsg is intentionally separate from reader activation messages.
// Generation and source bind it to the document that requested the navigation.
type ScriptNavigateMsg struct {
	Href, Mode, Source string
	Navigation         uint64
}

func (l *Loop) CommitFragment(generation uint64, raw string) {
	commit := l.locationCommit.Add(1)
	task := func() {
		if l.page != nil && generation == l.navGen.Load() && commit == l.locationCommit.Load() && !l.page.killed && strings.SplitN(raw, "#", 2)[0] == strings.SplitN(l.page.Base, "#", 2)[0] {
			l.page.Base = raw
			l.page.Sandbox.SetLocation(raw)
		}
	}
	if !l.tryPost(task) {
		go l.post(task)
	}
}
