// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package browsercmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A state file is whatever was left on disk: loadState must bound what it
// returns, and a payload written by stateSink must read back unchanged even
// over a longer, older file.
func FuzzLoadState(f *testing.F) {
	f.Add([]byte(stateMagic+"5\nhello"), []byte(`{"version":1}`))
	f.Add([]byte(stateMagic+"99999999999999999999\n"), []byte{})
	f.Add([]byte(stateMagic+"-1\n"), []byte("x"))
	f.Add([]byte(stateMagic+"3\nab"), []byte("\n"))
	f.Add([]byte("glynx-state v2 1\nx"), []byte(stateMagic))
	f.Add([]byte{}, []byte{})
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, onDisk, payload []byte) {
		path := filepath.Join(dir, "state")
		if err := os.WriteFile(path, onDisk, 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, err := loadState(file)
		if err != nil && data != nil {
			t.Fatalf("an unreadable file still returned %d bytes", len(data))
		}
		if len(data) > maxStatePayload || len(data) > len(onDisk) {
			t.Fatalf("loaded %d bytes from a %d-byte file (cap %d)", len(data), len(onDisk), maxStatePayload)
		}
		if len(payload) > maxStatePayload {
			return
		}
		if err := stateSink(file)(payload); err != nil {
			t.Fatal(err)
		}
		back, err := loadState(file)
		if err != nil || !bytes.Equal(back, payload) {
			t.Fatalf("wrote %q over %q, read back %q, %v", payload, onDisk, back, err)
		}
	})
}
