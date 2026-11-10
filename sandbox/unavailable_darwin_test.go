// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package sandbox

import (
	"errors"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
)

// When sandbox-exec cannot apply a policy, a required profile refuses to run
// and a best-effort one carries on unconfined; neither replaces the process.
func TestReexecWithoutSandboxExec(t *testing.T) {
	available := Available
	Available = func() bool { return false }
	t.Cleanup(func() { Available = available })
	p, _ := profile.Builtin("locked")
	if err := Reexec(p, "", "", []string{SandboxedArg}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("required: %v, want ErrUnavailable", err)
	}
	p.Enforcement = profile.EnforceBestEffort
	if err := Reexec(p, "", "", []string{SandboxedArg}); err != nil {
		t.Fatalf("best-effort: %v", err)
	}
	if got := Probe(); got["files"] != "" || got["process"] != "" {
		t.Fatalf("Probe claims OS strength without sandbox-exec: %v", got)
	}
}

// The helper's policy denies by default, grants no network, and writes
// nowhere but its media directory.
func TestHelperPolicyIsNarrow(t *testing.T) {
	policy := HelperPolicy("/Applications/x/ntbrowser-media", "/private/tmp/nt-media")
	if !strings.Contains(policy, "(deny default)") {
		t.Fatal("not deny-by-default:\n" + policy)
	}
	for _, line := range strings.Split(policy, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "(allow") {
			continue
		}
		if strings.Contains(line, "network") {
			t.Errorf("grants network: %s", line)
		}
		if strings.Contains(line, "file-write") && !strings.Contains(line, "/private/tmp/nt-media") && !strings.Contains(line, "/dev/null") {
			t.Errorf("grants a write outside the media directory: %s", line)
		}
	}
}
