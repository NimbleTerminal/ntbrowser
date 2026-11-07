// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"testing"

	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/jsvm"
)

func TestMessageTaskBudgetAndCancellationBoundary(t *testing.T) {
	l := New(context.Background(), Config{Script: jsvm.Config{MaxTimers: 1}}, make(chanSink, 8))
	h := (*host)(l)
	fn := func(goja.Value, ...goja.Value) (goja.Value, error) { return goja.Undefined(), nil }
	id, ok := h.QueueTask(fn)
	if !ok {
		t.Fatal("queue task")
	}
	h.ClearTimer(id)
	if len(l.timers) != 1 {
		t.Fatal("clearTimeout canceled private task")
	}
	if _, ok := h.SetTimer(0, false, fn, nil); ok {
		t.Fatal("task bypassed timer budget")
	}
	h.ClearTask(id)
	if len(l.timers) != 0 {
		t.Fatal("private task not canceled")
	}
	id, ok = h.SetTimer(0, false, fn, nil)
	if !ok {
		t.Fatal("timer budget not released")
	}
	h.ClearTask(id)
	if len(l.timers) != 1 {
		t.Fatal("private cancellation touched page timer")
	}
	h.ClearTimer(id)
}
