// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package devtools defines the host-only, transport-independent inspection API.
// A Session ID identifies a connection target; it is not an authentication token.
package devtools

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const Version = 1
const MaxItems = 50
const MaxEvents = 256
const MaxText = 4096

var revisions atomic.Uint64

func NextRevision() uint64 { return revisions.Add(1) }

type Request struct {
	Version  int    `json:"version"`
	Session  string `json:"session"`
	ID       string `json:"id,omitempty"`
	Method   string `json:"method"`
	Frame    uint64 `json:"frame,omitempty"`
	Revision uint64 `json:"revision,omitempty"`
	Node     uint64 `json:"node,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	After    uint64 `json:"after,omitempty"`
}
type Frame struct {
	ID                 uint64 `json:"id"`
	Revision           uint64 `json:"revision"`
	Parent             uint64 `json:"parent"`
	URL                string `json:"url"`
	Nodes              int    `json:"nodes"`
	DOMBytes           int    `json:"dom_bytes"`
	Scripts            int    `json:"scripts"`
	Timers             int    `json:"timers"`
	AnimationCallbacks int    `json:"animation_callbacks"`
	PendingScripts     int    `json:"pending_scripts"`
	Loaded             bool   `json:"loaded"`
	Disabled           bool   `json:"disabled"`
}
type Node struct {
	ID         uint64            `json:"id"`
	Tag        string            `json:"tag"`
	Text       string            `json:"text,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Children   int               `json:"children"`
}
type Snapshot struct {
	Frames []Frame `json:"frames"`
	Policy string  `json:"policy"`
}
type DOM struct {
	Frame    uint64 `json:"frame"`
	Revision uint64 `json:"revision"`
	Node     Node   `json:"node"`
	Children []Node `json:"children"`
	Next     int    `json:"next"` // -1 ends pagination
}
type Event struct {
	Sequence uint64          `json:"sequence"`
	Time     time.Time       `json:"time"`
	Kind     string          `json:"kind"`
	Data     json.RawMessage `json:"data"`
}
type Events struct {
	Dropped uint64  `json:"dropped"`
	Items   []Event `json:"items"`
	Next    uint64  `json:"next"`
	Lost    uint64  `json:"lost"`
}
type Response struct {
	Version      int       `json:"version"`
	Session      string    `json:"session"`
	ID           string    `json:"id,omitempty"`
	Error        string    `json:"error,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	Snapshot     *Snapshot `json:"snapshot,omitempty"`
	DOM          *DOM      `json:"dom,omitempty"`
	Events       *Events   `json:"events,omitempty"`
}
type Inspector func(context.Context, Request) (Response, error)
type Session struct {
	dropped         uint64
	mu              sync.Mutex
	id              string
	enabled, closed bool
	inspect         Inspector
	slots           chan struct{}
	events          []Event
	sequence        uint64
}

func New(enabled bool, inspect Inspector) *Session {
	return &Session{id: rand.Text(), enabled: enabled, inspect: inspect, slots: make(chan struct{}, 8)}
}
func (s *Session) ID() string { return s.id }
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.events = nil
}
func (s *Session) Do(ctx context.Context, r Request) Response {
	out := Response{Version: Version, Session: s.id, ID: Clip(r.ID, 64)}
	fail := func(code string) Response {
		return Response{Version: Version, Session: s.id, ID: Clip(r.ID, 64), Error: code}
	}
	s.mu.Lock()
	allowed := s.enabled && !s.closed
	s.mu.Unlock()
	if !allowed {
		return fail("disabled")
	}
	if r.Version != Version {
		return fail("unsupported_version")
	}
	if r.Session != s.id {
		return fail("wrong_session")
	}
	if len(r.ID) > 64 || r.Offset < 0 || r.Offset > 100000 || r.Limit < 0 || r.Limit > MaxItems {
		return fail("invalid_request")
	}
	if r.Limit == 0 {
		r.Limit = MaxItems
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return fail("busy")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return fail("cancelled")
	}
	switch r.Method {
	case "capabilities":
		out.Capabilities = []string{"snapshot", "dom", "events"}
	case "events":
		s.mu.Lock()
		e := Events{Next: r.After, Dropped: s.dropped}
		if len(s.events) > 0 && r.After < s.events[0].Sequence-1 {
			e.Lost = s.events[0].Sequence - 1 - r.After
		}
		for _, event := range s.events {
			if event.Sequence > r.After && len(e.Items) < r.Limit {
				event.Data = append(json.RawMessage(nil), event.Data...)
				e.Items = append(e.Items, event)
				e.Next = event.Sequence
			}
		}
		s.mu.Unlock()
		out.Events = &e
	case "snapshot", "dom":
		if s.inspect == nil {
			return fail("unavailable")
		}
		if r.Method == "dom" && r.Revision == 0 {
			return fail("revision_required")
		}
		result, err := s.inspect(ctx, r)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return fail("timeout")
			}
			return fail("unavailable")
		}
		out.Snapshot, out.DOM, out.Error = result.Snapshot, result.DOM, result.Error
	default:
		return fail("unsupported_method")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fail("closed")
	}
	return out
}

func Clip(s string, n int) string {
	if len(s) <= n {
		return strings.ToValidUTF8(s, "�")
	}
	s = s[:n]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}
func URL(raw string) string {
	u, err := url.Parse(Clip(raw, 4096))
	if err != nil {
		return "[invalid URL]"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return Clip(u.String(), 1024)
}
func failure(raw string) string {
	for _, label := range []string{"response too large", "denied by policy", "deadline exceeded", "timeout", "integrity", "cancelled", "canceled"} {
		if strings.Contains(strings.ToLower(Clip(raw, 4096)), label) {
			return label
		}
	}
	if raw != "" {
		return "request failed"
	}
	return ""
}

// Observe accepts the same host observations as JSONL capture, but publishes only
// selected metadata. It never retains raw HTML, headers, bodies or JS objects.
func (s *Session) Observe(kind string, data any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.closed {
		return
	}
	allowed := map[string]bool{"console": true, "network": true, "network_error": true, "redirect": true, "script_result": true, "script_request_denied": true}
	if !allowed[kind] {
		return
	}
	source := map[string]any{}
	switch data := data.(type) {
	case map[string]any:
		source = data
	case map[string]string:
		for k, v := range data {
			source[k] = v
		}
	default:
		s.dropped++
		return
	}
	fields := map[string]any{}
	for k, v := range source {
		switch k {
		case "url", "origin", "final_url", "from", "source", "destination":
			if text, ok := v.(string); ok {
				fields[k] = URL(text)
			}
		case "error", "reason":
			if text, ok := v.(string); ok {
				fields[k] = failure(text)
			}
		case "Level", "Line", "level", "line", "method", "resource", "content_type", "result":
			if text, ok := v.(string); ok {
				fields[k] = Clip(text, 2048)
			}
		case "status", "kind", "duration_ms", "frame", "revision":
			switch v := v.(type) {
			case int:
				fields[k] = v
			case int64:
				fields[k] = v
			case uint64:
				fields[k] = v
			case float64:
				fields[k] = v
			}
		}
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return
	}
	if len(b) > MaxText {
		b = []byte(`{"truncated":true}`)
	}
	s.sequence++
	e := Event{s.sequence, time.Now().UTC(), kind, b}
	if len(s.events) == MaxEvents {
		copy(s.events, s.events[1:])
		s.events[len(s.events)-1] = e
	} else {
		s.events = append(s.events, e)
	}
}
