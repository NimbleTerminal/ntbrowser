// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import "nimbleterminal.dev/nthtml/document"

// Snapshot on the owner loop, using the same defaults as window.matchMedia.
// These preferences select CSS branches; they grant no input or script rights.
func (l *Loop) renderState() document.RenderState {
	media := l.mediaState()
	motion, hover := "no-preference", "hover"
	if media.ReducedMotion == "reduce" {
		motion = "reduce"
	}
	if media.Hover == "none" {
		hover = "none"
	}
	return document.RenderState{Media: document.MediaPreferences{
		ReducedMotion: motion, Hover: hover, Scripting: media.Scripting,
	}}
}
