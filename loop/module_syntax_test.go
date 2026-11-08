// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/profile"
)

// TestDynamicImportScriptIsSkippedAsUnsupported loads a prerendered page whose
// classic bootstrap script uses import(). The page must still render and the
// reader must see a warning naming the unsupported feature, not a
// "SyntaxError" blaming the page.
func TestDynamicImportScriptIsSkippedAsUnsupported(t *testing.T) {
	p, _ := profile.Builtin("browse")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: p, Width: 400}, sink)
	go l.Run()
	l.LoadPage(`<p id="p">prerendered</p><script>
	{
		Promise.all([
			import("./_app/immutable/entry/start.js"),
			import("./_app/immutable/entry/app.js")
		]).then(([kit, app]) => kit.start(app));
	}
	</script><script>console.log("later script ran")</script>`, "https://example.test/", nil)
	var logs []LogMsg
	waitFor(t, sink, func(m tea.Msg) bool {
		lm, ok := m.(LogMsg)
		if ok {
			logs = append(logs, lm)
		}
		return ok && lm.Line == "later script ran"
	})
	waitFor(t, sink, func(m tea.Msg) bool { f, ok := m.(FrameMsg); return ok && f.Result != nil })
	if len(logs) != 2 {
		t.Fatalf("want one warning and one console line, got %+v", logs)
	}
	lm := logs[0]
	if lm.Level != "warn" || !strings.Contains(lm.Line, "import()") || !strings.Contains(lm.Line, "https://example.test/#inline-0") || strings.Contains(lm.Line, "SyntaxError") {
		t.Fatalf("unexpected report: %+v", lm)
	}
}

// TestExcludedGlobalIsWarnedNotErrored: the loop reports a script stopped by a
// design-excluded global at warn level, keeping error for real page faults.
func TestExcludedGlobalIsWarnedNotErrored(t *testing.T) {
	p, _ := profile.Builtin("browse")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: p, Width: 400}, sink)
	go l.Run()
	l.LoadPage(`<p>page</p><script>new XMLHttpRequest</script><script>undefinedFunction()</script>`, "https://example.test/", nil)
	var logs []LogMsg
	waitFor(t, sink, func(m tea.Msg) bool {
		if lm, ok := m.(LogMsg); ok {
			logs = append(logs, lm)
		}
		return len(logs) == 2
	})
	if logs[0].Level != "warn" || !strings.Contains(logs[0].Line, "script uses XMLHttpRequest, which this browser does not provide at https://example.test/#inline-0:1:") {
		t.Fatalf("unexpected first report: %+v", logs[0])
	}
	if logs[1].Level != "error" || !strings.Contains(logs[1].Line, "uncaught ReferenceError: undefinedFunction is not defined") {
		t.Fatalf("unexpected second report: %+v", logs[1])
	}
}
