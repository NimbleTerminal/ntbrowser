// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package devtools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func request(s *Session, method string) Request {
	return Request{Version: Version, Session: s.ID(), Method: method}
}
func TestSessionGateAndCapabilities(t *testing.T) {
	calls := 0
	inspect := func(context.Context, Request) (Response, error) { calls++; return Response{}, nil }
	s := New(false, inspect)
	if s.Do(context.Background(), request(s, "snapshot")).Error != "disabled" || calls != 0 {
		t.Fatal("closed gate entered inspector")
	}
	s = New(true, inspect)
	for _, tc := range []struct {
		r    Request
		code string
	}{
		{Request{Version: 2, Session: s.ID(), Method: "snapshot"}, "unsupported_version"},
		{Request{Version: 1, Session: "other", Method: "snapshot"}, "wrong_session"},
		{request(s, "eval"), "unsupported_method"},
		{request(s, "dom"), "revision_required"},
		{Request{Version: 1, Session: s.ID(), Method: "events", Limit: 51}, "invalid_request"},
	} {
		if got := s.Do(context.Background(), tc.r); got.Error != tc.code {
			t.Fatal(got, tc.code)
		}
	}
	if calls != 0 {
		t.Fatal("rejected method invoked inspector")
	}
	out := s.Do(context.Background(), request(s, "capabilities"))
	if strings.Join(out.Capabilities, ",") != "snapshot,dom,events" {
		t.Fatal(out)
	}
	s.Close()
	if s.Do(context.Background(), request(s, "snapshot")).Error != "disabled" {
		t.Fatal("closed session usable")
	}
}
func TestEventRingRedactionAndCopies(t *testing.T) {
	s := New(true, nil)
	s.Observe("document", map[string]string{"html": "SECRET"})
	for i := 0; i < 300; i++ {
		s.Observe("network", map[string]any{"url": "https://user:pass@site.test/a?token=SECRET#SECRET", "error": "denied by policy https://site.test/?secret=SECRET", "headers": "SECRET", "body": "SECRET", "status": 403})
	}
	r := request(s, "events")
	out := s.Do(context.Background(), r)
	if len(out.Events.Items) != 50 || out.Events.Lost != 44 || out.Events.Items[0].Sequence != 45 {
		t.Fatal(out.Events)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "pass") {
		t.Fatal("sensitive network data exposed", string(b))
	}
	out.Events.Items[0].Data[0] = 'x'
	if next := s.Do(context.Background(), r); next.Events.Items[0].Data[0] != '{' {
		t.Fatal("response shared event memory")
	}
	r.After = out.Events.Next
	if next := s.Do(context.Background(), r); next.Events.Items[0].Sequence != 95 || next.Events.Lost != 0 {
		t.Fatal(next)
	}
}
func TestOutstandingRequestsBoundedAndCloseRevokesReply(t *testing.T) {
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	s := New(true, func(ctx context.Context, r Request) (Response, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return Response{Snapshot: &Snapshot{Policy: "secret"}}, nil
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	})
	var wg sync.WaitGroup
	replies := make(chan Response, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); replies <- s.Do(context.Background(), request(s, "snapshot")) }()
	}
	for i := 0; i < 8; i++ {
		<-entered
	}
	if s.Do(context.Background(), request(s, "snapshot")).Error != "busy" {
		t.Fatal("unbounded queue")
	}
	s.Close()
	close(release)
	wg.Wait()
	for i := 0; i < 8; i++ {
		r := <-replies
		if r.Error != "closed" || r.Snapshot != nil {
			t.Fatal("closed reply retained data", r)
		}
	}
}
func TestWireRoundTripAndCancellation(t *testing.T) {
	s := New(true, nil)
	raw, _ := json.Marshal(request(s, "capabilities"))
	var r Request
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if got := s.Do(context.Background(), r); got.Error != "" {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := s.Do(ctx, r); got.Error != "cancelled" {
		t.Fatal(got)
	}
}

type forbiddenMarshaler struct{}

func (forbiddenMarshaler) MarshalJSON() ([]byte, error) { panic("inspection invoked custom marshaler") }
func TestObservationNeverInvokesCustomMarshaler(t *testing.T) {
	s := New(true, nil)
	s.Observe("console", forbiddenMarshaler{})
	s.Observe("network", map[string]any{"url": forbiddenMarshaler{}, "status": 200, "headers": forbiddenMarshaler{}})
	r := s.Do(context.Background(), request(s, "events"))
	if r.Events.Dropped != 1 || len(r.Events.Items) != 1 {
		t.Fatal(r)
	}
}
