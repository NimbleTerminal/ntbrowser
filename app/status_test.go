// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"errors"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/loop"
)

// What a status may be replaced by, and how it is drawn, follows from its
// kind. Its text decides neither, so text that merely looks like a failure
// (a file name, a console line) gains no priority.
func TestStatusKindNotTextDecidesPriorityAndStyle(t *testing.T) {
	for _, text := range []string{"[error] spoof", "load failed: spoof", "blocked: spoof", "render: spoof", "paint: spoof", "focus: spoof"} {
		s := status{kind: statusPlain, text: text}
		if s.sticky() {
			t.Errorf("%q: a plain status survives frames", text)
		}
		if line := statusLine(s, 40); strings.Contains(line, "\x1b[") {
			t.Errorf("%q: a plain status is drawn as an alert: %q", text, line)
		}
	}
	if !(status{kind: statusFailure, text: "anything"}).sticky() {
		t.Error("a failure does not survive frames")
	}
	for _, k := range []statusKind{statusFailure, statusError, statusRender, statusPaint, statusWarning} {
		if line := statusLine(status{kind: k, text: "plain words"}, 40); !strings.Contains(line, "\x1b[") {
			t.Errorf("kind %d is drawn without alert styling: %q", k, line)
		}
	}
}

func TestStatusKindsAssignedBySource(t *testing.T) {
	m := newModel(t, NavOff)
	for _, c := range []struct {
		level string
		want  statusKind
	}{{"error", statusFailure}, {"warn", statusWarning}, {"log", statusPlain}, {"info", statusPlain}} {
		m.Update(loop.LogMsg{Level: c.level, Line: "x"})
		if m.status.kind != c.want || m.status.text != "["+c.level+"] x" {
			t.Errorf("console %s: %+v", c.level, m.status)
		}
	}
	m.Update(pageLoadedMsg{seq: m.navSeq, err: errors.New("boom")})
	if m.status != (status{statusFailure, "load failed: boom"}) {
		t.Errorf("load failure: %+v", m.status)
	}
	m.Update(pageLoadedMsg{seq: m.navSeq, line: "load failed: report.zip · 2 KB · can't display"})
	if m.status.kind != statusPlain {
		t.Errorf("a described download took kind %d from its text", m.status.kind)
	}
	m.Update(loop.NavigateMsg{Href: "https://page.test/b"})
	if m.status.kind != statusFailure || !strings.HasPrefix(m.status.text, "blocked:") {
		t.Errorf("navigation off: %+v", m.status)
	}
	m.Update(loop.FrameMsg{Gen: 99, Err: errors.New("layout")})
	if m.status != (status{statusRender, "render: layout"}) {
		t.Errorf("render failure: %+v", m.status)
	}
}
