// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

//go:build !nocjk

package loop

import (
	"nimbleterminal.dev/ntfonts/cjk/scregular"
	"nimbleterminal.dev/nthtml/document"
)

// Simplified Chinese Regular (about 4 MB; bold CJK draws Regular). Build with
// -tags nocjk to leave it out.
func init() {
	for _, family := range scregular.Fallbacks() {
		fallbackFaces = append(fallbackFaces, document.FallbackFace{Family: family, Source: scregular.Embedded})
	}
}
