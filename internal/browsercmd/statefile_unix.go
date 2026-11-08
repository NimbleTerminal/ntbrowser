// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

//go:build unix

package browsercmd

import "syscall"

// noFollow refuses a symlink in the store's place when opening it.
const noFollow = syscall.O_NOFOLLOW
