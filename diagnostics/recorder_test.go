// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRecordsEscapePageTextAndClose(t *testing.T) {
	var b bytes.Buffer
	r := New(&b)
	r.Record("console", "\x1b]52;c;bad\a\n{\"kind\":\"fake\"}")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r.Record("late", nil)
	if bytes.ContainsRune(b.Bytes(), '\x1b') || bytes.Count(b.Bytes(), []byte{'\n'}) != 1 {
		t.Fatal("unescaped terminal control or injected record")
	}
	var v map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(b.Bytes()), &v); err != nil {
		t.Fatal(err)
	}
	if v["kind"] != "console" {
		t.Fatal(v)
	}
}

func TestBoundedFile(t *testing.T) {
	var b bytes.Buffer
	r := &Recorder{queue: make(chan []byte), done: make(chan struct{})}
	go r.write(&b)
	record := []byte(strings.Repeat("x", 1<<20))
	for i := 0; i < 40; i++ {
		r.queue <- record
	}
	close(r.queue)
	<-r.done
	if b.Len() > MaxBytes || !strings.Contains(b.String(), "diagnostic byte limit") {
		t.Fatal(b.Len())
	}
}

func TestQueueDropsWithoutBlocking(t *testing.T) {
	r := &Recorder{queue: make(chan []byte, 1)}
	r.Record("one", 1)
	r.Record("two", 2)
	if r.dropped != 1 {
		t.Fatal(r.dropped)
	}
	<-r.queue
	r.Record("three", 3)
	var v struct{ Dropped int }
	json.Unmarshal(<-r.queue, &v)
	if v.Dropped != 1 {
		t.Fatal(v)
	}
}

type badWriter struct{}

func (badWriter) Write([]byte) (int, error) { return 0, errors.New("disk failed") }
func TestWriteErrorReported(t *testing.T) {
	r := New(badWriter{})
	r.Record("one", 1)
	if err := r.Close(); err == nil {
		t.Fatal("lost disk error")
	}
}

func TestLiveObserverWithoutFile(t *testing.T) {
	var kinds []string
	r := NewObserved(nil, func(kind string, data any) { kinds = append(kinds, kind) })
	r.Record("console", map[string]string{"Line": "hello"})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r.Record("console", "after close")
	if len(kinds) != 1 || kinds[0] != "console" {
		t.Fatal(kinds)
	}
}

func TestObserverAndFileReceiveSameObservation(t *testing.T) {
	var file bytes.Buffer
	seen := ""
	r := NewObserved(&file, func(kind string, data any) { seen = kind })
	r.Record("console", map[string]string{"Line": "hello"})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if seen != "console" || !strings.Contains(file.String(), `"Line":"hello"`) {
		t.Fatal(seen, file.String())
	}
}
