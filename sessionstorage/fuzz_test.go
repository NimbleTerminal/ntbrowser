// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package sessionstorage

import (
	"bytes"
	"testing"
)

// The localStorage file is read back from disk before lockdown.
func FuzzRestore(f *testing.F) {
	seed := New(0, 0)
	if a := seed.Open("https://a.test/"); a != nil {
		_ = a.Set("k", "v")
	}
	f.Add(seed.Snapshot())
	f.Add([]byte(`{"version":1,"origins":[{"origin":"https://a.test","entries":[["AAA=","!!"]]}]}`))
	f.Add([]byte(`{"version":1,"origins":[{"origin":"file:///x","entries":[["",""]]}]}`))
	f.Add([]byte(`{"version":7}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		s := New(0, 0)
		if err := s.Restore(data); err != nil {
			if empty := New(0, 0).Snapshot(); !bytes.Equal(s.Snapshot(), empty) {
				t.Fatalf("a rejected snapshot left data: %s", s.Snapshot())
			}
			return
		}
		snap := s.Snapshot()
		if len(snap) > MaxSnapshotBytes {
			t.Fatalf("snapshot is %d bytes, cap %d", len(snap), MaxSnapshotBytes)
		}
		r := New(0, 0)
		if err := r.Restore(snap); err != nil {
			t.Fatalf("own snapshot rejected: %v", err)
		}
		if again := r.Snapshot(); !bytes.Equal(again, snap) {
			t.Fatalf("snapshot does not round-trip:\n%s\n%s", snap, again)
		}
	})
}
