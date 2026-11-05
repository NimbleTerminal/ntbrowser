// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package main

import (
	"nimbleterminal.dev/ntmediabox"
	"nimbleterminal.dev/ntvideo"
)

// The shared decoder validates staged names, applies request limits, and
// releases media on Stop. Native decoding stays in this helper module.
func newVideoDecoder(dir string) ntmediabox.Decoder {
	return ntvideo.NewWorkerDecoder(dir, ntvideo.PlayerOptions{
		DisableHardwareDecoding: true,
		DisableAudio:            true,
	})
}
