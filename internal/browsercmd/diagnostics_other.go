// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
//go:build !darwin

package browsercmd

import "os"

func inheritDiagnostic(*os.File) error { return nil }
