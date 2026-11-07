// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package diagnostics records host-only observations, never commands. Records
// contain untrusted page data; consumers must not treat it as instructions.
package diagnostics

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

const MaxBytes = 32 << 20
const MaxRecord = 4 << 20

type Recorder struct {
	observer func(string, any)
	mu       sync.Mutex
	queue    chan []byte
	done     chan struct{}
	closed   bool
	dropped  uint64
	seq      uint64
	err      error
}

// New takes a preopened writer. Call Close before closing the writer.
func New(w io.Writer) *Recorder {
	return NewObserved(w, nil)
}

// NewObserved fans host observations to an observer and an optional file writer.
// The observer must be bounded, nonblocking, and must not call back into Recorder.
func NewObserved(w io.Writer, observer func(string, any)) *Recorder {
	r := &Recorder{observer: observer, queue: make(chan []byte, 8), done: make(chan struct{})}
	if w == nil {
		r.queue = nil
		close(r.done)
	} else {
		go r.write(w)
	}
	return r
}

// Record copies data to JSON before returning. A full queue drops observations
// rather than blocking the VM. All records carry a cumulative dropped count.
func (r *Recorder) Record(kind string, data any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if r.observer != nil {
		r.observer(kind, data)
	}
	if r.queue == nil {
		return
	}
	r.seq++
	b, err := json.Marshal(struct {
		Version  int       `json:"version"`
		Sequence uint64    `json:"sequence"`
		Time     time.Time `json:"time"`
		Kind     string    `json:"kind"`
		Dropped  uint64    `json:"dropped"`
		Data     any       `json:"data"`
	}{1, r.seq, time.Now().UTC(), kind, r.dropped, data})
	if err != nil || len(b) > MaxRecord {
		r.dropped++
		return
	}
	select {
	case r.queue <- append(b, '\n'):
	default:
		r.dropped++
	}
}

func (r *Recorder) write(w io.Writer) {
	defer close(r.done)
	total := 0
	stopped := false
	for b := range r.queue {
		if stopped {
			continue
		}
		if total+len(b) > MaxBytes-256 {
			b = []byte("{\"version\":1,\"kind\":\"limit\",\"data\":\"diagnostic byte limit reached; recording stopped\"}\n")
			stopped = true
		}
		n, err := w.Write(b)
		total += n
		if err == nil && n != len(b) {
			err = io.ErrShortWrite
		}
		if err != nil {
			r.mu.Lock()
			r.err = err
			r.mu.Unlock()
			stopped = true
		}
	}
}

func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		if r.queue != nil {
			close(r.queue)
		}
	}
	r.mu.Unlock()
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}
