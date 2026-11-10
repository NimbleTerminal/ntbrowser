// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package cookies

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"
)

const snapshotVersion = 1

// MaxSnapshotBytes bounds a snapshot handed to Restore: every cookie at its
// byte cap plus structure.
const MaxSnapshotBytes = MaxCookies*(MaxCookieBytes+512) + 4096

var ErrSnapshot = errors.New("cookie snapshot rejected")

type snapshot struct {
	Version int              `json:"version"`
	Cookies []snapshotCookie `json:"cookies"`
}

// snapshotCookie is one persistent cookie: the attributes the store keeps,
// never the request that set it.
type snapshotCookie struct {
	Name, Value  string
	Domain, Path string
	HostOnly     bool
	Secure       bool
	HttpOnly     bool
	SameSite     int
	Expires      time.Time
}

// OnChange registers one callback the store invokes after each stored write,
// outside its lock and on the writer's goroutine; nil removes it. Set it
// before the store is shared.
func (s *Store) OnChange(fn func()) {
	if s != nil {
		s.changed = fn
	}
}

// Snapshot serializes the cookies that outlive the session: those with an
// expiry that has not passed. Session cookies (no expiry) are left out, as a
// browser drops them at exit. Order follows creation order.
func (s *Store) Snapshot() []byte {
	out := snapshot{Version: snapshotVersion}
	if s != nil {
		s.mu.Lock()
		now := s.now()
		s.prune(now)
		list := make([]entry, 0, len(s.entries))
		for _, e := range s.entries {
			if !e.expires.IsZero() {
				list = append(list, e)
			}
		}
		s.mu.Unlock()
		sort.Slice(list, func(i, j int) bool { return list[i].order < list[j].order })
		for _, e := range list {
			out.Cookies = append(out.Cookies, snapshotCookie{Name: e.cookie.Name, Value: e.cookie.Value, Domain: e.domain, Path: e.path, HostOnly: e.hostOnly, Secure: e.cookie.Secure, HttpOnly: e.cookie.HttpOnly, SameSite: int(e.cookie.SameSite), Expires: e.expires})
		}
	}
	data, _ := json.Marshal(out)
	return data
}

// Restore loads a Snapshot into an empty store under the store's own caps:
// expired, malformed or over-cap cookies are dropped, never the whole file.
// Unknown versions, oversize input and malformed JSON are rejected and leave
// the store empty.
func (s *Store) Restore(data []byte) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]entry)
	s.sequence = 0
	if len(data) == 0 {
		return nil
	}
	if len(data) > MaxSnapshotBytes {
		return fmt.Errorf("%w: %d bytes", ErrSnapshot, len(data))
	}
	var in snapshot
	if err := json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("%w: %v", ErrSnapshot, err)
	}
	if in.Version != snapshotVersion {
		return fmt.Errorf("%w: version %d", ErrSnapshot, in.Version)
	}
	now := s.now()
	perDomain := map[string]int{}
	for _, c := range in.Cookies {
		if c.Name == "" || c.Domain == "" || c.Path == "" || c.Expires.IsZero() || !now.Before(c.Expires) || len(c.Name)+len(c.Value) > MaxCookieBytes {
			continue
		}
		if c.SameSite < 0 || c.SameSite > int(http.SameSiteNoneMode) {
			c.SameSite = 0 // unset, as Set stores a cookie without the attribute
		}
		if c.SameSite == int(http.SameSiteNoneMode) && !c.Secure {
			continue // Set refuses it; a file must not bring it back
		}
		if len(s.entries) >= MaxCookies || perDomain[c.Domain] >= MaxDomainCookies {
			break
		}
		key := c.Domain + "\x00" + c.Path + "\x00" + c.Name // the store's own key shape
		if _, dup := s.entries[key]; dup {
			continue
		}
		if c.Expires.After(now.Add(400 * 24 * time.Hour)) {
			c.Expires = now.Add(400 * 24 * time.Hour)
		}
		s.sequence++
		s.entries[key] = entry{cookie: http.Cookie{Name: c.Name, Value: c.Value, Secure: c.Secure, HttpOnly: c.HttpOnly, SameSite: http.SameSite(c.SameSite)}, domain: c.Domain, path: c.Path, hostOnly: c.HostOnly, expires: c.Expires, order: s.sequence}
		perDomain[c.Domain]++
	}
	return nil
}
