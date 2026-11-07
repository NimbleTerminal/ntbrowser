// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package browsercmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnosticFileClosedByDefaultAndExclusive(t *testing.T) {
	st := state{}
	if f, err := diagnosticFile(&st, false); err != nil || f != nil {
		t.Fatal(f, err)
	}
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	c, err := parseArgs([]string{"--diagnostics", path, "https://example.test/"})
	if err != nil || !c.Profile.Diagnostics || c.Profile.Name != "browse+flags" {
		t.Fatal(c, err)
	}
	st.Config = c
	f, err := diagnosticFile(&st, false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := f.Stat()
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if _, err := diagnosticFile(&st, false); err == nil {
		t.Fatal("overwrote existing capture")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	st.Config.Diagnostics = link
	if _, err := diagnosticFile(&st, false); err == nil {
		t.Fatal("followed symlink")
	}
}
