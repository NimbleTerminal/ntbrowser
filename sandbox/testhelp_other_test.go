// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

//go:build !darwin

package sandbox

import (
	"os/exec"
	"testing"
)

const childSandboxedEnv = "NT_SANDBOX_UNUSED"

func childCommand(t *testing.T, exe, mode, tmp string) *exec.Cmd {
	t.Helper()
	return exec.Command(exe)
}
