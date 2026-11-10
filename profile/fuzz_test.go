// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package profile

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// A profile file is the user's, but it is parsed before anything is
// confined and decides every gate: whatever Parse accepts must be a profile
// that says the same thing when written back and read again.
func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"base":"browse","navigate":"confirm"}`))
	f.Add([]byte(`{"name":"x","scripts":"external","script_grants":[{"source":"https://a.test","destination":"https://b.test"}]}`))
	f.Add([]byte(`{"base":"glynx","hosts":[".example.com"],"frames":"any"}`))
	f.Add([]byte(`{"base":"nope"}`))
	f.Add([]byte(`{"unknown":1}`))
	for _, name := range []string{"locked", "reader", "browse", "glynx"} {
		if p, ok := Builtin(name); ok {
			data, _ := json.Marshal(p)
			f.Add(data)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data)
		if err != nil {
			if !reflect.DeepEqual(p, Profile{}) {
				t.Fatalf("a rejected file still produced a profile: %+v", p)
			}
			return
		}
		if err := p.check(); err != nil {
			t.Fatalf("Parse accepted a profile its own check refuses: %v", err)
		}
		out, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(out)
		if err != nil {
			t.Fatalf("accepted profile does not re-parse: %v\n%s", err, out)
		}
		// Compared as written: an empty list and an absent one are the same file.
		if back, _ := json.Marshal(again); !bytes.Equal(out, back) {
			t.Fatalf("profile changed across a round trip:\n%s\n%s", out, back)
		}
	})
}
