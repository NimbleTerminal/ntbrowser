// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package cookies owns a bounded in-memory browser-session cookie store.
package cookies

import (
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

const MaxCookieBytes = 4096
const MaxCookies = 256
const MaxDomainCookies = 50

type entry struct {
	cookie       http.Cookie
	domain, path string
	hostOnly     bool
	expires      time.Time
	order        uint64
}

type Store struct {
	mu       sync.Mutex
	entries  map[string]entry
	sequence uint64
	now      func() time.Time
	changed  func() // set before use; called after each stored write, outside the lock
}

// New creates a separate browser session with no persistent backing.
func New() *Store { return &Store{entries: make(map[string]entry), now: time.Now} }
func host(u *url.URL) string {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	h, err := idna.Lookup.ToASCII(strings.ToLower(u.Hostname()))
	if err != nil {
		return ""
	}
	return h
}
func domainMatch(h, d string) bool {
	return h == d || (net.ParseIP(h) == nil && strings.HasSuffix(h, "."+d))
}
func pathMatch(p, d string) bool {
	return p == d || strings.HasPrefix(p, d) && (strings.HasSuffix(d, "/") || len(p) > len(d) && p[len(d)] == '/')
}
func path(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	return p
}
func (s *Store) prune(now time.Time) {
	for k, e := range s.entries {
		if !e.expires.IsZero() && !now.Before(e.expires) {
			delete(s.entries, k)
		}
	}
}

// Set accepts one Set-Cookie value. Script writes may neither create nor
// overwrite HttpOnly cookies. Invalid or disallowed writes are ignored.
func (s *Store) Set(u *url.URL, raw string, script bool) {
	if s.set(u, raw, script) && s.changed != nil {
		s.changed()
	}
}

// set stores one cookie and reports whether the store changed.
func (s *Store) set(u *url.URL, raw string, script bool) bool {
	if s == nil || len(raw) > MaxCookieBytes {
		return false
	}
	h := host(u)
	if h == "" {
		return false
	}
	c, err := http.ParseSetCookie(raw)
	if err != nil || c.Partitioned {
		return false
	}
	if script && c.HttpOnly || c.Secure && u.Scheme != "https" || c.SameSite == http.SameSiteNoneMode && !c.Secure {
		return false
	}
	d := h
	hostOnly := c.Domain == ""
	if !hostOnly {
		d, err = idna.Lookup.ToASCII(strings.ToLower(strings.TrimPrefix(c.Domain, ".")))
		if err != nil || d == "" || strings.HasSuffix(d, ".") || !domainMatch(h, d) {
			return false
		}
		suffix, _ := publicsuffix.PublicSuffix(d)
		if suffix == d && net.ParseIP(d) == nil {
			return false
		}
	}
	p := c.Path
	if !strings.HasPrefix(p, "/") {
		p = path(u)
		i := strings.LastIndex(p, "/")
		if i <= 0 {
			p = "/"
		} else {
			p = p[:i]
		}
	}
	name := strings.ToLower(c.Name)
	if strings.HasPrefix(name, "__secure-") && !c.Secure {
		return false
	}
	if strings.HasPrefix(name, "__host-") && (!c.Secure || !hostOnly || c.Path != "/") {
		return false
	}
	if (strings.HasPrefix(name, "__http-") || strings.HasPrefix(name, "__host-http-")) && (!c.Secure || !c.HttpOnly || script) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.prune(now)
	key := d + "\x00" + p + "\x00" + c.Name
	old, exists := s.entries[key]
	if exists && script && old.cookie.HttpOnly {
		return false
	}
	// An insecure origin must not shadow or delete a secure cookie.
	if u.Scheme != "https" {
		for _, e := range s.entries {
			if e.cookie.Secure && e.cookie.Name == c.Name && (domainMatch(d, e.domain) || domainMatch(e.domain, d)) && pathMatch(p, e.path) {
				return false
			}
		}
	}
	expires := c.Expires
	if c.MaxAge < 0 {
		delete(s.entries, key)
		return false
	}
	if c.MaxAge > 0 {
		expires = now.Add(time.Duration(min(c.MaxAge, 400*24*60*60)) * time.Second)
	}
	if !expires.IsZero() && !now.Before(expires) {
		delete(s.entries, key)
		return false
	}
	if expires.After(now.Add(400 * 24 * time.Hour)) {
		expires = now.Add(400 * 24 * time.Hour)
	}
	if !exists {
		count := 0
		for _, e := range s.entries {
			if e.domain == d {
				count++
			}
		}
		if len(s.entries) >= MaxCookies || count >= MaxDomainCookies {
			return false
		}
		s.sequence++
		old.order = s.sequence
	}
	s.entries[key] = entry{cookie: *c, domain: d, path: p, hostOnly: hostOnly, expires: expires, order: old.order}
	return true
}

func (s *Store) selected(u *url.URL, script, crossSite, safeNavigation bool) []*http.Cookie {
	if s == nil {
		return nil
	}
	h := host(u)
	if h == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(s.now())
	var found []entry
	for _, e := range s.entries {
		if e.hostOnly && h != e.domain || !domainMatch(h, e.domain) || !pathMatch(path(u), e.path) || e.cookie.Secure && u.Scheme != "https" || script && e.cookie.HttpOnly {
			continue
		}
		if !script && crossSite {
			if e.cookie.SameSite == http.SameSiteStrictMode {
				continue
			}
			if e.cookie.SameSite != http.SameSiteNoneMode && !safeNavigation {
				continue
			}
		}
		found = append(found, e)
	}
	sort.Slice(found, func(i, j int) bool {
		if len(found[i].path) != len(found[j].path) {
			return len(found[i].path) > len(found[j].path)
		}
		return found[i].order < found[j].order
	})
	out := make([]*http.Cookie, 0, len(found))
	for _, e := range found {
		out = append(out, &http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value, Quoted: e.cookie.Quoted})
	}
	return out
}
func (s *Store) Document(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	var parts []string
	for _, c := range s.selected(u, true, false, false) {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, "; ")
}
func (s *Store) SetDocument(rawURL, raw string) {
	u, err := url.Parse(rawURL)
	if err == nil {
		s.Set(u, raw, true)
	}
}

// SameSite uses scheme plus registrable domain; IP addresses are their own site.
func SameSite(a, b *url.URL) bool {
	x, y := host(a), host(b)
	if x == "" || y == "" || a.Scheme != b.Scheme {
		return false
	}
	site := func(h string) string {
		d, err := publicsuffix.EffectiveTLDPlusOne(h)
		if err != nil {
			return h
		}
		return d
	}
	return site(x) == site(y)
}

// sameOrigin treats omitted default ports as their explicit equivalents.
func sameOrigin(a, b *url.URL) bool {
	if host(a) == "" || host(b) == "" || host(a) != host(b) || a.Scheme != b.Scheme {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return port(a) == port(b)
}

// Transport adds cookies only to vetted requests. Subrequests must be exactly
// same-origin with the document. Navigation may use destination cookies, with
// SameSite restrictions relative to the initiating page and redirect chain.
type Transport struct {
	Base       http.RoundTripper
	Store      *Store
	Origin     *url.URL
	Navigation func(*http.Request) bool
	Omit       func(*http.Request) bool
}

func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Omit != nil && t.Omit(req) {
		return t.Base.RoundTrip(req)
	}
	nav := t.Navigation != nil && t.Navigation(req)
	allowed := nav || sameOrigin(req.URL, t.Origin)
	if !allowed || t.Store == nil {
		return t.Base.RoundTrip(req)
	}
	cross := t.Origin != nil && !SameSite(t.Origin, req.URL)
	for prev := req.Response; prev != nil; {
		if !SameSite(prev.Request.URL, req.URL) {
			cross = true
		}
		prev = prev.Request.Response
	}
	clone := req.Clone(req.Context())
	clone.Header.Del("Cookie")
	for _, c := range t.Store.selected(req.URL, false, cross, nav && (req.Method == "GET" || req.Method == "HEAD")) {
		clone.AddCookie(c)
	}
	resp, err := t.Base.RoundTrip(clone)
	if err == nil {
		for _, raw := range resp.Header.Values("Set-Cookie") {
			t.Store.Set(req.URL, raw, false)
		}
	}
	return resp, err
}
