// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

// Script-created images use the subresources gate, including when detached.
// The session enforces exactly the same transport, timeout, concurrency,
// count and decoded-pixel limits used for images discovered by the renderer.
func (l *Loop) updateImages() {
	page := l.page
	if page == nil || page.killed {
		return
	}
	more := page.Sandbox.UpdateImages(func(src string) (int, int, bool) {
		if !l.cfg.Profile.AllowsSubresources() || page.subres == nil {
			return 0, 0, true
		}
		return page.subres.ImageStatus(src)
	})
	if more {
		l.tryPost(func() {})
	} // a full inbox already guarantees another turn
}
