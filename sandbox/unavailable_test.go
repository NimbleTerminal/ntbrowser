// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package sandbox

import (
	"errors"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
)

func TestProbeReportsWhatThisMachineCanHold(t *testing.T) {
	got, want := Probe(), strengths(Available())
	if len(got) != len(want) {
		t.Fatalf("Probe %v, want %v", got, want)
	}
	for gate, s := range want {
		if got[gate] != s {
			t.Errorf("%s: %q, want %q", gate, got[gate], s)
		}
	}
}

// The helper is found only beside this executable, never through PATH; the
// test binary has no such sibling.
func TestHelperPathNeedsTheSibling(t *testing.T) {
	path, err := HelperPath()
	if err == nil || path != "" || !strings.Contains(err.Error(), "media=video needs ntbrowser-media") {
		t.Fatalf("HelperPath = %q, %v", path, err)
	}
}

// Without the helper binary nothing starts, whatever the enforcement mode.
func TestStartHelperWithoutBinaryFails(t *testing.T) {
	for _, mode := range []profile.Enforcement{profile.EnforceBestEffort, profile.EnforceRequired} {
		p, _ := profile.Builtin("browse")
		p.Enforcement = mode
		dir := t.TempDir()
		h, err := StartHelper(p, dir+"/ntbrowser-media", dir)
		if err == nil {
			h.Close()
			t.Fatalf("%s: a missing helper started", mode)
		}
		if h != nil {
			t.Fatalf("%s: a failed start returned a helper", mode)
		}
		_ = errors.Is(err, ErrUnavailable) // either refusal is acceptable; starting is not
	}
}
