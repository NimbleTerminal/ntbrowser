// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package cookies

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/origin"
)

func uri(s string) *url.URL { u, _ := url.Parse(s); return u }
func TestDocumentScopeAndExpiry(t *testing.T) {
	s := New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.SetDocument("https://www.example.com/a/page", "host=one")
	s.SetDocument("https://www.example.com/a/page", "shared=two; Domain=example.com; Path=/; Secure")
	for _, tc := range []struct{ url, want string }{
		{"https://www.example.com/a/x", "host=one; shared=two"},
		{"https://www.example.com/ab", "shared=two"},
		{"https://other.example.com/a/x", "shared=two"},
		{"https://notexample.com/a/x", ""},
		{"http://other.example.com/", ""},
		{"file:///a/x", ""},
	} {
		if got := s.Document(tc.url); got != tc.want {
			t.Fatalf("%s: %q want %q", tc.url, got, tc.want)
		}
	}
	s.SetDocument("https://www.example.com/a/page", "host=gone; Max-Age=1; Expires=Wed, 01 Jan 2020 00:00:00 GMT")
	if !strings.Contains(s.Document("https://www.example.com/a/page"), "host=gone") {
		t.Fatal("Max-Age must take precedence")
	}
	now = now.Add(2 * time.Second)
	if strings.Contains(s.Document("https://www.example.com/a/page"), "host=") {
		t.Fatal("expired cookie retained")
	}
	s.SetDocument("https://www.example.com/", "shared=; Domain=example.com; Path=/; Max-Age=0; Secure")
	if s.Document("https://www.example.com/") != "" {
		t.Fatal("deletion failed")
	}
}
func TestCookieSecurityAttributes(t *testing.T) {
	s := New()
	u := uri("https://www.example.com/")
	for _, raw := range []string{"x=1; Domain=com", "x=1; Domain=other.com", "x=1; Domain=example.com.", "x=1; HttpOnly", "__Secure-x=1", "__Host-x=1; Secure", "__Host-x=1; Secure; Path=/; Domain=example.com", "x=1; SameSite=None", "x=1; Secure; Partitioned"} {
		s.Set(u, raw, true)
		if got := s.Document(u.String()); got != "" {
			t.Fatalf("accepted %s: %s", raw, got)
		}
	}
	s.Set(u, "secret=server; HttpOnly; Secure; Path=/", false)
	s.Set(u, "secret=script; Path=/", true)
	s.Set(u, "secret=gone; Path=/; Max-Age=0", true)
	if s.Document(u.String()) != "" {
		t.Fatal("HttpOnly exposed")
	}
	if c := s.selected(u, false, false, false); len(c) != 1 || c[0].Value != "server" {
		t.Fatal("HttpOnly overwritten")
	}
	s.Set(uri("http://www.example.com/"), "secret=plain; Path=/", false)
	if c := s.selected(u, false, false, false); c[0].Value != "server" {
		t.Fatal("secure cookie overwritten by HTTP")
	}
	s.Set(u, "__Host-ok=yes; Secure; Path=/", true)
	if s.Document(u.String()) != "__Host-ok=yes" {
		t.Fatal("valid prefix rejected")
	}
	s.Set(uri("http://www.example.com/"), "bad=1; Secure", false)
	if strings.Contains(s.Document(u.String()), "bad=") {
		t.Fatal("insecure Secure cookie")
	}
}
func TestCookieBudgetsAndIsolation(t *testing.T) {
	s := New()
	u := uri("https://a.example/")
	s.Set(u, "large="+strings.Repeat("x", MaxCookieBytes), false)
	if len(s.entries) != 0 {
		t.Fatal("oversize cookie")
	}
	for i := 0; i < MaxCookies+20; i++ {
		s.Set(uri(fmt.Sprintf("https://site%d.example/", i)), "x=1", false)
	}
	if len(s.entries) != MaxCookies {
		t.Fatal(len(s.entries))
	}
	s = New()
	for i := 0; i < MaxDomainCookies+10; i++ {
		s.Set(u, fmt.Sprintf("x%d=1", i), false)
	}
	if len(s.entries) != MaxDomainCookies {
		t.Fatal(len(s.entries))
	}
	if New().Document(u.String()) != "" {
		t.Fatal("sessions shared")
	}
}
func TestSameSiteSelection(t *testing.T) {
	s := New()
	u := uri("https://a.example.com/")
	for _, c := range []string{"strict=1; SameSite=Strict", "lax=1; SameSite=Lax", "default=1", "none=1; SameSite=None; Secure"} {
		s.Set(u, c, false)
	}
	if got := s.selected(u, false, true, false); len(got) != 1 || got[0].Name != "none" {
		t.Fatal(got)
	}
	if len(s.selected(u, false, true, true)) != 3 {
		t.Fatal("cross-site safe navigation")
	}
	if !SameSite(u, uri("https://b.example.com/")) || SameSite(u, uri("http://a.example.com/")) || SameSite(u, uri("https://example.net/")) {
		t.Fatal("schemeful site mismatch")
	}
}

func TestIDNAAndSecureCookiePathOverlay(t *testing.T) {
	s := New()
	s.SetDocument("https://bücher.example/a", "x=1; Domain=xn--bcher-kva.example; Path=/")
	if s.Document("https://xn--bcher-kva.example/") != "x=1" {
		t.Fatal("IDNA cookie mismatch")
	}
	s.Set(uri("https://example.com/login"), "sid=secure; Secure; Path=/login", false)
	s.Set(uri("http://example.com/login/child"), "sid=attacker; Path=/login/child", false)
	if got := s.Document("https://example.com/login/child"); got != "sid=secure" {
		t.Fatal(got)
	}
}

func TestSameOriginDefaultPorts(t *testing.T) {
	if !origin.Same(uri("https://EXAMPLE.com/"), uri("https://example.com:443/")) || origin.Same(uri("https://example.com/"), uri("https://example.com:444/")) {
		t.Fatal("origin port mismatch")
	}
}

// TestSnapshotRestoreKeepsPersistentCookiesOnly: cookies with an expiry
// survive a snapshot round trip with their attributes and order; session
// cookies, expired cookies and over-cap entries do not. OnChange fires only
// for stored writes.
func TestSnapshotRestoreKeepsPersistentCookiesOnly(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	s := New()
	s.now = func() time.Time { return now }
	changes := 0
	s.OnChange(func() { changes++ })
	u, _ := url.Parse("https://shop.example.test/cart/")
	s.Set(u, "sid=abc; Max-Age=86400; Secure; HttpOnly; SameSite=Lax; Path=/", false)
	s.Set(u, "pref=dark; Expires="+now.Add(48*time.Hour).UTC().Format(http.TimeFormat)+"; Domain=example.test", false)
	s.Set(u, "temp=1", false)                                             // session cookie
	s.Set(u, "gone=1; Max-Age=1", false)                                  // expires before the restore below
	s.Set(u, "bad=1; Secure", false)                                      // ignored: Secure needs https... it is https, so stored
	s.Set(&url.URL{Scheme: "http", Host: "x.test"}, "s=1; Secure", false) // refused: Secure over http
	if changes != 5 {
		t.Fatalf("changes %d", changes)
	}
	data := s.Snapshot()
	if strings.Contains(string(data), `"temp"`) || !strings.Contains(string(data), `"sid"`) || !strings.Contains(string(data), `"pref"`) {
		t.Fatalf("snapshot: %s", data)
	}
	r := New()
	r.now = func() time.Time { return now.Add(2 * time.Second) }
	if err := r.Restore(data); err != nil {
		t.Fatal(err)
	}
	if got := r.Document("https://shop.example.test/cart/x"); got != "pref=dark" { // sid is HttpOnly: not visible to script
		t.Fatalf("document after restore: %q", got)
	}
	var header []string
	for _, c := range r.selected(u, false, false, true) {
		header = append(header, c.Name)
	}
	if strings.Join(header, ",") != "sid,pref" && strings.Join(header, ",") != "pref,sid" {
		t.Fatalf("restored cookies: %v", header)
	}
	if _, ok := r.entries["example.test\x00/\x00gone"]; ok {
		t.Fatal("expired cookie restored")
	}
	if strings.Contains(r.Document("http://shop.example.test/"), "sid") {
		t.Fatal("Secure cookie served over http after restore")
	}
	// Garbage, versions and oversize input start empty; caps hold.
	for _, raw := range []string{"nope", `{"version":9,"cookies":[]}`, strings.Repeat("x", MaxSnapshotBytes+1)} {
		x := New()
		x.Set(u, "k=v; Max-Age=60", false)
		if err := x.Restore([]byte(raw)); err == nil || len(x.entries) != 0 {
			t.Fatalf("%.10q: %v", raw, err)
		}
	}
	many := snapshot{Version: 1}
	for i := 0; i < MaxDomainCookies+5; i++ {
		many.Cookies = append(many.Cookies, snapshotCookie{Name: fmt.Sprint("c", i), Value: "v", Domain: "a.test", Path: "/", HostOnly: true, Expires: now.Add(time.Hour)})
	}
	encoded, _ := json.Marshal(many)
	x := New()
	x.now = func() time.Time { return now }
	if err := x.Restore(encoded); err != nil || len(x.entries) != MaxDomainCookies {
		t.Fatalf("domain cap on restore: %d %v", len(x.entries), err)
	}
}
