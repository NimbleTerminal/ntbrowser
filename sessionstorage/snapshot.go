// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package sessionstorage

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// snapshotVersion is the on-disk format version; anything else is ignored.
const snapshotVersion = 1

// MaxSnapshotBytes bounds a snapshot a host may hand to Restore: the largest
// total quota, base64-expanded, plus structure.
const MaxSnapshotBytes = MaxTotalBytes*2 + 1<<20

var ErrSnapshot = errors.New("storage snapshot rejected")

type snapshot struct {
	Version int              `json:"version"`
	Origins []snapshotOrigin `json:"origins"`
}
type snapshotOrigin struct {
	Origin  string      `json:"origin"`
	Entries [][2]string `json:"entries"` // base64 key, base64 value; raw UTF-16LE bytes
}

// OnChange registers one callback the store invokes after every successful
// Set, Remove or Clear, on the caller's goroutine. A persistent host uses it
// to schedule a Snapshot; nil removes the callback.
func (s *Session) OnChange(fn func()) {
	if s != nil {
		s.changed = fn
	}
}

func (s *Session) notify() {
	if s != nil && s.changed != nil {
		s.changed()
	}
}

// Snapshot serializes every origin in insertion order. Keys and values are
// raw UTF-16LE byte strings, so they travel base64-encoded.
func (s *Session) Snapshot() []byte {
	out := snapshot{Version: snapshotVersion}
	if s != nil {
		for _, origin := range s.order {
			b := s.origins[origin]
			if b == nil {
				continue
			}
			so := snapshotOrigin{Origin: origin, Entries: make([][2]string, 0, len(b.keys))}
			for _, k := range b.keys {
				so.Entries = append(so.Entries, [2]string{base64.StdEncoding.EncodeToString([]byte(k)), base64.StdEncoding.EncodeToString([]byte(b.values[k]))})
			}
			out.Origins = append(out.Origins, so)
		}
	}
	data, _ := json.Marshal(out)
	return data
}

// Restore loads a Snapshot into an empty session under the session's own
// quotas and caps: entries that do not fit are dropped, never the whole
// store. Unknown versions, oversize input and malformed JSON are rejected
// and leave the session empty. Origins go through the same parser as Open.
func (s *Session) Restore(data []byte) error {
	if s == nil {
		return nil
	}
	s.Clear()
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
	changed := s.changed
	s.changed = nil // loading is not a change
	defer func() { s.changed = changed }()
	for _, so := range in.Origins {
		area := s.Open(so.Origin + "/")
		if area == nil {
			continue
		}
		for _, e := range so.Entries {
			k, err1 := base64.StdEncoding.DecodeString(e[0])
			v, err2 := base64.StdEncoding.DecodeString(e[1])
			if err1 != nil || err2 != nil || len(k)%2 != 0 || len(v)%2 != 0 {
				continue
			}
			if err := area.Set(string(k), string(v)); err != nil {
				break // this origin's quota or key cap; the rest of the file still loads
			}
		}
	}
	return nil
}
