// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import "nimbleterminal.dev/nthtml/document"

// fallbackFaces are the host fonts nthtml tries for characters the page's
// faces lack. Build-tagged files add to it in init; it is read-only after.
var fallbackFaces []document.FallbackFace
