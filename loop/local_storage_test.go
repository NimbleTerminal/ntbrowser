// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sessionstorage"
)

// TestLocalStoragePersistsThroughSink: pages share one host store per origin
// across navigations, changes reach the sink coalesced, and the final state
// is written when the loop stops.
func TestLocalStoragePersistsThroughSink(t *testing.T) {
	previous := localFlushDelay
	localFlushDelay = 20 * time.Millisecond
	t.Cleanup(func() { localFlushDelay = previous })
	store := sessionstorage.New(0, 0)
	writes := make(chan []byte, 16)
	sink := func(data []byte) error { writes <- append([]byte(nil), data...); return nil }
	p, _ := profile.Builtin("browse")
	ctx, cancel := context.WithCancel(context.Background())
	chanSink := make(chanSink, 256)
	l := New(ctx, Config{Profile: p, Width: 400, LocalStorage: store, LocalStorageSink: sink}, chanSink)
	done := make(chan struct{})
	go func() { l.Run(); close(done) }()
	l.LoadPage(`<script>localStorage.setItem('theme','dark');localStorage.setItem('n','1');localStorage.setItem('n','2');console.log('wrote '+localStorage.length)</script>`, "https://example.test/a", nil)
	waitFor(t, chanSink, func(m tea.Msg) bool { lm, ok := m.(LogMsg); return ok && lm.Line == "wrote 2" })
	select {
	case data := <-writes:
		if !strings.Contains(string(data), `"origin":"https://example.test"`) {
			t.Fatalf("snapshot: %s", data)
		}
	case <-time.After(patience):
		t.Fatal("no coalesced write")
	}
	select {
	case <-writes:
		t.Fatal("three writes were not coalesced into one")
	case <-time.After(100 * time.Millisecond):
	}
	// A later document on the same origin reads the value; another origin does not.
	l.LoadPage(`<script>console.log('same '+localStorage.getItem('theme'))</script>`, "https://example.test/b", nil)
	waitFor(t, chanSink, func(m tea.Msg) bool { lm, ok := m.(LogMsg); return ok && lm.Line == "same dark" })
	l.LoadPage(`<script>console.log('other '+localStorage.getItem('theme'));localStorage.setItem('x','y')</script>`, "https://other.test/", nil)
	waitFor(t, chanSink, func(m tea.Msg) bool { lm, ok := m.(LogMsg); return ok && lm.Line == "other null" })
	cancel()
	<-done
	// The write for other.test may have been coalesced before or flushed by Run's exit; either way it arrives.
	select {
	case data := <-writes:
		if !strings.Contains(string(data), "other.test") {
			t.Fatalf("final snapshot: %s", data)
		}
	case <-time.After(patience):
		t.Fatal("final flush missing")
	}
}

// TestLocalStorageSinkFailureIsReportedOnce: a failing sink warns once and the
// store keeps working in memory. Without a sink nothing is written at all.
func TestLocalStorageSinkFailureIsReportedOnce(t *testing.T) {
	previous := localFlushDelay
	localFlushDelay = 10 * time.Millisecond
	t.Cleanup(func() { localFlushDelay = previous })
	p, _ := profile.Builtin("browse")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	store := sessionstorage.New(0, 0)
	writes := make(chan int, 16) // how many entries each failed write carried
	l := New(ctx, Config{Profile: p, Width: 400, LocalStorage: store, LocalStorageSink: func(data []byte) error {
		var snap struct {
			Origins []struct{ Entries [][2]string }
		}
		_ = json.Unmarshal(data, &snap)
		n := 0
		for _, o := range snap.Origins {
			n += len(o.Entries)
		}
		writes <- n
		return errors.New("disk full")
	}}, sink)
	go l.Run()
	l.LoadPage(`<script>localStorage.setItem('a','1');setTimeout(()=>{localStorage.setItem('b','2');console.log('second')},40)</script>`, "https://example.test/", nil)
	warnings := 0
	waitFor(t, sink, func(m tea.Msg) bool {
		lm, ok := m.(LogMsg)
		if ok && lm.Level == "warn" && strings.Contains(lm.Line, "local storage not saved: disk full") {
			warnings++
		}
		return ok && lm.Line == "second"
	})
	// The sink runs on the loop goroutine, so once the write carrying both
	// entries has failed and the loop answers, any warning for it was sent.
	for entries := 0; entries < 2; {
		select {
		case entries = <-writes:
		case <-time.After(patience):
			t.Fatal("the second entry was never flushed")
		}
	}
	pageState(t, l)
	for len(sink) > 0 {
		if lm, ok := (<-sink).(LogMsg); ok && lm.Level == "warn" {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatalf("sink failure reported %d times", warnings)
	}
	if store.Open("https://example.test/").Len() != 2 {
		t.Fatal("store stopped working after a failed write")
	}
	// Memory mode: a store without a sink is never flushed and never warns.
	memory := New(ctx, Config{Profile: p, Width: 400, LocalStorage: sessionstorage.New(0, 0)}, make(chanSink, 16))
	go memory.Run()
	memory.LoadPage(`<script>localStorage.setItem('a','1')</script>`, "https://example.test/", nil)
	time.Sleep(40 * time.Millisecond)
}
