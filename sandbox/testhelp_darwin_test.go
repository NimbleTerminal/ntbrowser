// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package sandbox

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
)

const childSandboxedEnv = "NT_SANDBOX_REEXECED"

// On macOS the sandbox is applied from outside, as the program does it.
func childCommand(t *testing.T, exe, mode, tmp string) *exec.Cmd {
	t.Helper()
	if mode != "locked" {
		return exec.Command(exe)
	}
	p, _ := profile.Builtin("locked")
	p.Files = profile.FilesTemp
	real, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sandboxExec, "-p", Policy(p, "", real, exe), exe)
	cmd.Env = []string{childSandboxedEnv + "=1"}
	return cmd
}

// The marker alone must not be believed: an unconfined process that claims to
// be sandboxed fails under required.
func TestApplyProvesConfinement(t *testing.T) {
	p, _ := profile.Builtin("locked")
	if _, err := Apply(p, "", "", true); err == nil {
		t.Fatal("unconfined process accepted as sandboxed")
	}
}

// Inside another sandbox sandbox-exec exits with an error; that must read as
// unavailable, not kill the program.
func TestFailingSandboxExecIsUnavailable(t *testing.T) {
	if !applies(sandboxExec) {
		t.Skip("already inside a sandbox")
	}
	if applies("/usr/bin/false") {
		t.Fatal("a failing tool read as available")
	}
}

func TestDiagnosticPolicyGrantsOnlyExactFileWhenEnabled(t *testing.T) {
	p, _ := profile.Builtin("browse")
	path := "/private/tmp/trace.jsonl"
	if strings.Contains(Policy(p, "", "", "/bin/test", path), path) {
		t.Fatal("closed gate granted file")
	}
	p.Diagnostics = true
	policy := Policy(p, "", "", "/bin/test", path)
	if !strings.Contains(policy, `(allow file-write-data (literal "/private/tmp/trace.jsonl"))`) {
		t.Fatal(policy)
	}
	if strings.Contains(policy, `(subpath "/private/tmp`) {
		t.Fatal("granted directory")
	}
}
