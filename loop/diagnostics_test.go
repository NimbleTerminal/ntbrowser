// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"bytes"
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/diagnostics"
	"nimbleterminal.dev/ntbrowser/profile"
)

func TestDiagnosticsObserveVMWithoutPageAuthority(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var b bytes.Buffer
		trace := diagnostics.New(&b)
		p, _ := profile.Builtin("browse")
		p.Diagnostics = enabled
		ctx, cancel := context.WithCancel(context.Background())
		sink := make(chanSink, 256)
		l := New(ctx, Config{Profile: p, Trace: trace, Width: 400}, sink)
		go l.Run()
		l.LoadPage(`<p id="p">before</p><script>document.getElementById("p").textContent="after";console.log("diagnostic="+typeof diagnostics);function rejectedProbe(){return Error("rejection probe")}Promise.reject(rejectedProbe());throw Error("diagnostic probe")</script>`, "https://example.test/", nil)
		waitFor(t, sink, func(m tea.Msg) bool { _, ok := m.(FrameMsg); return ok })
		cancel()
		trace.Close()
		if !enabled {
			if b.Len() != 0 {
				t.Fatal("closed profile leaked diagnostics")
			}
			continue
		}
		s := b.String()
		for _, want := range []string{`"kind":"document"`, `"kind":"snapshot"`, `"kind":"script_result"`, `diagnostic=undefined`, `diagnostic probe`, `Unhandled promise rejection: Error: rejection probe at rejectedProbe (https://example.test/#inline-0:1:`, `after`} {
			if !strings.Contains(s, want) {
				t.Fatalf("missing %s", want)
			}
		}
	}
}
