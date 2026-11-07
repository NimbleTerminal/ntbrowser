// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

// completeDocument runs on the loop after resource/script settlements. The
// first render discovers CSS backgrounds; all supported resource loads settle
// (success or failure) before load fires. Timers and fetch do not block load.
func (l *Loop) completeDocument() {
	p := l.page
	if p == nil || p.killed || p.Sandbox.Disabled() || !p.loaded || p.loadComplete || p.firstFrame || p.dynamicPending > 0 {
		return
	}
	for _, f := range p.frames {
		if f.pending {
			return
		}
	}
	if p.subres != nil {
		p.subres.Discover(p.Tree.LinkedStyles(), p.Tree.ImageSources(), p.backgrounds)
		if p.subres.Pending() {
			return
		}
	}
	p.loadComplete = true
	if l.cfg.Profile.ScriptsOn() {
		l.report(p.Sandbox.SetDocumentReady("complete"))
		l.report(p.Sandbox.DispatchDocumentLoad())
	}
	l.send(documentLoadedMsg{})
}
