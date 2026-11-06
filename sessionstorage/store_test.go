// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package sessionstorage

import (
	"fmt"
	"testing"
)

func TestOriginAndLifetime(t *testing.T) {
	s := New(0, 0)
	a := s.Open("https://EXAMPLE.test:443/a")
	if err := a.Set("key", "value"); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://example.test/b?q=1#x", "https://example.test:443/"} {
		v, ok := s.Open(u).Get("key")
		if !ok || v != "value" {
			t.Fatal(u, v)
		}
	}
	for _, u := range []string{"http://example.test/", "https://example.test:444/", "https://other.example.test/"} {
		if _, ok := s.Open(u).Get("key"); ok {
			t.Fatal("origin leaked", u)
		}
	}
	for _, u := range []string{"file:///a", "data:text/html,hello", "https://u:p@example.test/"} {
		if s.Open(u) != nil {
			t.Fatal("unsupported origin", u)
		}
	}
	if _, ok := New(0, 0).Open("https://example.test/").Get("key"); ok {
		t.Fatal("session leak")
	}
	s.Clear()
	if a.Len() != 0 {
		t.Fatal("shutdown retained data")
	}
}
func TestAtomicQuotasAndAccounting(t *testing.T) {
	s := New(8, 12)
	a := s.Open("https://a.test/")
	b := s.Open("https://b.test/")
	if a.Set("aa", "123456") != nil {
		t.Fatal("exact quota rejected")
	}
	if a.Set("aa", "1234567") != ErrQuota {
		t.Fatal("oversize update accepted")
	}
	if got, _ := a.Get("aa"); got != "123456" {
		t.Fatal("failed write changed old value")
	}
	if b.Set("bb", "12") != nil || b.Set("cc", "3") != ErrQuota {
		t.Fatal("total quota bypassed")
	}
	a.Remove("aa")
	if b.Set("cc", "3") != nil {
		t.Fatal("delete failed to release quota")
	}
	b.Clear()
	if s.total != 0 || len(s.origins) != 0 {
		t.Fatal(s.total)
	}
}
func TestOriginAndKeyCaps(t *testing.T) {
	s := New(0, 0)
	for i := 0; i < MaxOrigins; i++ {
		if s.Open(fmt.Sprintf("https://a%d.test/", i)).Set("", "") != nil {
			t.Fatal(i)
		}
	}
	extra := s.Open("https://extra.test/")
	if extra.Set("", "") != ErrQuota {
		t.Fatal("origin cap bypassed")
	}
	s.Open("https://a0.test/").Clear()
	if extra.Set("", "") != nil {
		t.Fatal("empty origin not reclaimed")
	}
	s = New(0, 0)
	a := s.Open("https://a.test/")
	for i := 0; i < MaxKeys; i++ {
		if a.Set(fmt.Sprint(i), "") != nil {
			t.Fatal(i)
		}
	}
	if a.Set("overflow", "") != ErrQuota || a.Set("0", "new") != nil {
		t.Fatal("key cap or update broken")
	}
}

func TestRemovalReleasesKeyBackingReference(t *testing.T) {
	s := New(0, 0)
	a := s.Open("https://a.test/")
	a.Set("keep", "v")
	a.Set("removed", "v")
	b := s.origins[a.origin]
	a.Remove("removed")
	if b.keys[:cap(b.keys)][1] != "" {
		t.Fatal("deleted key retained outside slice length")
	}
}
