// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package main

import (
	"nimbleterminal.dev/ntbrowser/internal/browsercmd"
	"os"
)

func main() { os.Exit(browsercmd.Run(browsercmd.NTBrowser)) }
