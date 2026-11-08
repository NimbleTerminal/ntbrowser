// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package sessionstorage owns one browser session's origin-isolated string
// storage. Only the VM goroutine accesses it. Strings are opaque UTF-16 byte
// encodings supplied by the binding, so accounting includes keys and values
// and preserves lone surrogates without retaining a JavaScript runtime.
package sessionstorage

import (
	"errors"
	weburl "github.com/nlnwa/whatwg-url/url"
)

const DefaultBytes = 1 << 20
const DefaultTotalBytes = 8 << 20
const MaxBytes = 8 << 20
const MaxTotalBytes = 64 << 20
const MaxOrigins = 64
const MaxKeys = 1024

var ErrQuota = errors.New("session storage quota exceeded")

type bucket struct {
	values map[string]string
	keys   []string
	bytes  int
}
type Session struct {
	origins                  map[string]*bucket
	order                    []string // nonempty origins in first-write order, for stable snapshots
	total, limit, totalLimit int
	changed                  func()
}
type Area struct {
	session *Session
	origin  string
}

func New(perOrigin, total int) *Session {
	if perOrigin <= 0 {
		perOrigin = DefaultBytes
	}
	if total <= 0 {
		total = DefaultTotalBytes
	}
	return &Session{origins: map[string]*bucket{}, limit: min(perOrigin, MaxBytes), totalLimit: min(total, MaxTotalBytes)}
}
func (s *Session) Open(raw string) *Area {
	if s == nil {
		return nil
	}
	u, err := weburl.Parse(raw)
	if err != nil || (u.Scheme() != "http" && u.Scheme() != "https") || u.Host() == "" || u.Username() != "" || u.Password() != "" {
		return nil
	}
	return &Area{session: s, origin: u.Protocol() + "//" + u.Host()}
}
func (a *Area) Get(key string) (string, bool) {
	b := a.session.origins[a.origin]
	if b == nil {
		return "", false
	}
	v, ok := b.values[key]
	return v, ok
}
func (a *Area) Keys() []string {
	b := a.session.origins[a.origin]
	if b == nil {
		return nil
	}
	return append([]string(nil), b.keys...)
}
func (a *Area) Len() int {
	b := a.session.origins[a.origin]
	if b == nil {
		return 0
	}
	return len(b.keys)
}
func (a *Area) Set(key, value string) error {
	s := a.session
	b := s.origins[a.origin]
	delta := len(key) + len(value)
	oldBytes := 0
	exists := false
	if b != nil {
		old, ok := b.values[key]
		exists = ok
		if ok {
			delta -= len(key) + len(old)
		}
		oldBytes = b.bytes
	}
	if oldBytes+delta > s.limit || s.total+delta > s.totalLimit || b == nil && len(s.origins) >= MaxOrigins || b != nil && !exists && len(b.keys) >= MaxKeys {
		return ErrQuota
	}
	if b == nil {
		b = &bucket{values: map[string]string{}}
		s.origins[a.origin] = b
		s.order = append(s.order, a.origin)
	}
	if !exists {
		b.keys = append(b.keys, key)
	}
	b.values[key] = value
	b.bytes += delta
	s.total += delta
	s.notify()
	return nil
}

// forget drops an emptied origin from the order list.
func (s *Session) forget(origin string) {
	for i, o := range s.order {
		if o == origin {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}
func (a *Area) Remove(key string) {
	s := a.session
	b := s.origins[a.origin]
	if b == nil {
		return
	}
	value, ok := b.values[key]
	if !ok {
		return
	}
	delete(b.values, key)
	cost := len(key) + len(value)
	b.bytes -= cost
	s.total -= cost
	for i, k := range b.keys {
		if k == key {
			copy(b.keys[i:], b.keys[i+1:])
			b.keys[len(b.keys)-1] = ""
			b.keys = b.keys[:len(b.keys)-1]
			break
		}
	}
	if len(b.keys) == 0 {
		delete(s.origins, a.origin)
		s.forget(a.origin)
	}
	s.notify()
}
func (a *Area) Clear() {
	s := a.session
	if b := s.origins[a.origin]; b != nil {
		s.total -= b.bytes
		delete(s.origins, a.origin)
		s.forget(a.origin)
		s.notify()
	}
}
func (s *Session) Clear() {
	if s != nil {
		s.origins = map[string]*bucket{}
		s.order = nil
		s.total = 0
	}
}
