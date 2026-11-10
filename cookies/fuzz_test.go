// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package cookies

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

var fuzzNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func fuzzStore() *Store {
	s := New()
	s.now = func() time.Time { return fuzzNow }
	return s
}

// checkStore asserts what must hold for any store, however it was filled.
func checkStore(t *testing.T, s *Store) {
	t.Helper()
	if len(s.entries) > MaxCookies {
		t.Fatalf("%d cookies stored, cap %d", len(s.entries), MaxCookies)
	}
	perDomain := map[string]int{}
	for _, e := range s.entries {
		perDomain[e.domain]++
		if e.cookie.SameSite == http.SameSiteNoneMode && !e.cookie.Secure {
			t.Fatalf("cookie %q is SameSite=None without Secure", e.cookie.Name)
		}
		if len(e.cookie.Name)+len(e.cookie.Value) > MaxCookieBytes {
			t.Fatalf("cookie %q is %d bytes, cap %d", e.cookie.Name, len(e.cookie.Name)+len(e.cookie.Value), MaxCookieBytes)
		}
	}
	for d, n := range perDomain {
		if n > MaxDomainCookies {
			t.Fatalf("%d cookies for %q, cap %d", n, d, MaxDomainCookies)
		}
	}
	// What is persisted restores to the same persisted state.
	snap := s.Snapshot()
	if len(snap) > MaxSnapshotBytes {
		t.Fatalf("snapshot is %d bytes, cap %d", len(snap), MaxSnapshotBytes)
	}
	r := fuzzStore()
	if err := r.Restore(snap); err != nil {
		t.Fatalf("own snapshot rejected: %v\n%s", err, snap)
	}
	if again := r.Snapshot(); !bytes.Equal(again, snap) {
		t.Fatalf("snapshot does not round-trip:\n%s\n%s", snap, again)
	}
}

// A Set-Cookie header is server input, document.cookie is page input.
func FuzzSetCookie(f *testing.F) {
	f.Add("sid=1; Path=/; Secure; HttpOnly; SameSite=Lax", "https://www.example.com/a/b", false)
	f.Add("a=b; Domain=example.com; Max-Age=3600", "https://www.example.com/", true)
	f.Add("a=b; Domain=.com; Expires=Wed, 09 Jun 2027 10:18:14 GMT", "http://example.com/x", false)
	f.Add("__Host-a=b; SameSite=None", "https://[::1]:8443/", true)
	f.Add("a=\xff\x00; Path=//", "https://bücher.example/", false)
	f.Fuzz(func(t *testing.T, raw, page string, script bool) {
		u := uri(page)
		if u == nil {
			return
		}
		s := fuzzStore()
		s.Set(u, raw, script)
		for _, e := range s.entries {
			if script && e.cookie.HttpOnly {
				t.Fatalf("script stored an HttpOnly cookie from %q", raw)
			}
			if e.cookie.Secure && u.Scheme != "https" {
				t.Fatalf("%s stored a Secure cookie from %q", u.Scheme, raw)
			}
			if h := host(u); h == "" || !domainMatch(h, e.domain) {
				t.Fatalf("%q stored a cookie for %q from %q", u.Host, e.domain, raw)
			}
		}
		if doc := s.Document(page); strings.ContainsAny(doc, "\r\n\x00") {
			t.Fatalf("document.cookie carries control bytes: %q", doc)
		}
		checkStore(t, s)
	})
}

// The cookie file is read back from disk before lockdown.
func FuzzRestore(f *testing.F) {
	seed := fuzzStore()
	seed.Set(uri("https://www.example.com/"), "a=b; Max-Age=3600; Domain=example.com; Secure", false)
	f.Add(seed.Snapshot())
	f.Add([]byte(`{"version":1,"cookies":[{"name":"a","value":"b","domain":"x","path":"/","expires":"2027-01-01T00:00:00Z","same_site":9}]}`))
	f.Add([]byte(`{"version":2}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		s := fuzzStore()
		if err := s.Restore(data); err != nil {
			if len(s.entries) != 0 {
				t.Fatalf("a rejected snapshot left %d cookies", len(s.entries))
			}
			return
		}
		for _, e := range s.entries {
			if e.expires.IsZero() || !fuzzNow.Before(e.expires) {
				t.Fatalf("restored a session or expired cookie: %+v", e)
			}
		}
		checkStore(t, s)
	})
}

// A cookie file cannot bring back what Set refuses.
func TestRestoreRefusesSameSiteNoneWithoutSecure(t *testing.T) {
	s := fuzzStore()
	err := s.Restore([]byte(`{"version":1,"cookies":[
		{"Name":"bad","Value":"1","Domain":"example.com","Path":"/","SameSite":4,"Expires":"2027-01-01T00:00:00Z"},
		{"Name":"ok","Value":"1","Domain":"example.com","Path":"/","Secure":true,"SameSite":4,"Expires":"2027-01-01T00:00:00Z"}]}`))
	if err != nil || len(s.entries) != 1 {
		t.Fatalf("restored %d cookies, %v; want only the Secure one", len(s.entries), err)
	}
}
