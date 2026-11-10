// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package sessionstorage

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSnapshotRoundTripKeepsOrderAndRawUnits(t *testing.T) {
	s := New(0, 0)
	b := s.Open("https://b.test/x")
	a := s.Open("https://A.test:443/")
	_ = b.Set("k\x00\x00\xd8", "v\x00") // "k" then a lone surrogate U+D800, raw UTF-16LE bytes
	_ = a.Set("\x00\x01", "")
	_ = a.Set("z\x00", "1\x00")
	_ = a.Set("\x00\x01", "x\x00") // overwrite keeps position
	data := s.Snapshot()
	if !bytes.Contains(data, []byte(`"origin":"https://b.test"`)) || bytes.Index(data, []byte("b.test")) > bytes.Index(data, []byte("a.test")) {
		t.Fatalf("snapshot order: %s", data)
	}
	r := New(0, 0)
	if err := r.Restore(data); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Open("https://b.test/").Get("k\x00\x00\xd8"); got != "v\x00" {
		t.Fatalf("surrogate key lost: %q", got)
	}
	ra := r.Open("https://a.test/")
	if keys := ra.Keys(); len(keys) != 2 || keys[0] != "\x00\x01" || keys[1] != "z\x00" {
		t.Fatalf("key order: %q", keys)
	}
	if got, _ := ra.Get("\x00\x01"); got != "x\x00" {
		t.Fatal("overwrite lost")
	}
	if !bytes.Equal(r.Snapshot(), data) {
		t.Fatal("restore is not a fixpoint")
	}
	if _, ok := r.Open("http://b.test/").Get("k\x00\x00\xd8"); ok {
		t.Fatal("origin leaked across schemes")
	}
}

func TestRestoreIsBoundedAndQuotaChecked(t *testing.T) {
	s := New(8, 12)
	big := New(0, 0)
	_ = big.Open("https://a.test/").Set("aa", "123456")
	_ = big.Open("https://a.test/").Set("bb", "7") // does not fit the 8-byte origin quota after aa
	_ = big.Open("https://b.test/").Set("cc", "12")
	_ = big.Open("https://c.test/").Set("dd", "33") // total quota 12: aa(8)+cc(4) fill it
	if err := s.Restore(big.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Open("https://a.test/").Get("aa"); v != "123456" {
		t.Fatal("first entry missing")
	}
	if _, ok := s.Open("https://a.test/").Get("bb"); ok {
		t.Fatal("origin quota bypassed by restore")
	}
	if _, ok := s.Open("https://c.test/").Get("dd"); ok {
		t.Fatal("total quota bypassed by restore")
	}
	for _, raw := range []string{`{"version":2,"origins":[]}`, `not json`, `{"version":1,"origins":[{"origin":"file:///x","entries":[["YQ==","Yg=="]]}]}`, strings.Repeat("x", MaxSnapshotBytes+1)} {
		r := New(0, 0)
		_ = r.Open("https://keep.test/").Set("k", "v")
		err := r.Restore([]byte(raw))
		if strings.HasPrefix(raw, `{"version":1`) {
			if err != nil || r.Open("https://keep.test/").Len() != 0 || r.total != 0 {
				t.Fatalf("file origin: %v", err)
			}
			continue
		}
		if !errors.Is(err, ErrSnapshot) || r.total != 0 {
			t.Fatalf("%.20q: %v", raw, err)
		}
	}
	if err := New(0, 0).Restore(nil); err != nil {
		t.Fatal(err)
	}
	// Odd-length (non-UTF-16) entries are skipped, not stored.
	r := New(0, 0)
	_ = r.Restore([]byte(`{"version":1,"origins":[{"origin":"https://o.test","entries":[["YQ==","YmI="],["YWE=","YmI="]]}]}`))
	if r.Open("https://o.test/").Len() != 1 {
		t.Fatal("odd entry stored")
	}
}

func TestOnChangeFiresForWritesOnly(t *testing.T) {
	s := New(0, 0)
	calls := 0
	s.OnChange(func() { calls++ })
	a := s.Open("https://a.test/")
	_, _ = a.Get("x")
	a.Remove("x")
	a.Clear()
	if calls != 0 {
		t.Fatal("no-ops notified")
	}
	_ = a.Set("x", "1")
	_ = a.Set("x", "2")
	a.Remove("x")
	_ = a.Set("y", "1")
	a.Clear()
	if calls != 5 {
		t.Fatalf("calls %d", calls)
	}
	data := s.Snapshot()
	if err := s.Restore(data); err != nil || calls != 5 {
		t.Fatalf("restore notified: %d %v", calls, err)
	}
	s.OnChange(nil)
	_ = a.Set("z", "1")
	if calls != 5 {
		t.Fatal("removed callback fired")
	}
}
