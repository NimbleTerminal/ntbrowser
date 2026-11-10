// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/profile"
)

func TestAnimationMutationsAppearInUpcomingFrame(t *testing.T) {
	p, _ := profile.Builtin("browse")
	p.FrameBudgetMillis = 50
	_, sink := profileLoop(t, p, `<p id=p>before</p><script>requestAnimationFrame(()=>{document.getElementById("p").textContent="first";requestAnimationFrame(()=>document.getElementById("p").textContent="second")});</script>`)
	for _, want := range []string{"first", "second"} {
		f := waitFor(t, sink, func(m tea.Msg) bool { _, ok := m.(FrameMsg); return ok }).(FrameMsg)
		if f.Err != nil {
			t.Fatal(f.Err)
		}
		found := false
		for _, b := range f.Result.Boxes {
			if b.Text == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("upcoming frame missing %s", want)
		}
	}
}
func frameFixture(t *testing.T, p profile.Profile) *Loop {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l := New(ctx, Config{Profile: p}, make(chanSink, 64))
	tree, err := dom.Parse(strings.NewReader("<p>test</p>"), dom.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	sb := jsvm.New(jsvm.Config{}, tree, (*host)(l))
	t.Cleanup(sb.Close)
	l.page = &Page{Tree: tree, Sandbox: sb}
	return l
}
func TestAnimationCancellationAndRegistrationDuringBatch(t *testing.T) {
	p, _ := profile.Builtin("browse")
	p.FrameCallbacks = 2
	p.FrameBudgetMillis = 50
	l := frameFixture(t, p)
	h := (*host)(l)
	ran := []string{}
	var other int
	var firstTS float64
	h.RequestFrame(func(_ goja.Value, args ...goja.Value) (goja.Value, error) {
		firstTS = args[0].ToFloat()
		ran = append(ran, "a")
		h.CancelFrame(other)
		if h.RequestFrame(func(_ goja.Value, _ ...goja.Value) (goja.Value, error) { ran = append(ran, "next"); return nil, nil }) == 0 {
			t.Error("cancel did not release slot")
		}
		return nil, nil
	})
	other = h.RequestFrame(func(_ goja.Value, _ ...goja.Value) (goja.Value, error) {
		t.Error("cancelled callback ran")
		return nil, nil
	})
	if h.RequestFrame(func(goja.Value, ...goja.Value) (goja.Value, error) { return nil, nil }) != 0 {
		t.Fatal("callback cap bypassed")
	}
	l.runFrameCallbacks()
	if strings.Join(ran, ",") != "a" || len(l.page.animation) != 1 || firstTS < 0 {
		t.Fatal(ran, l.page.animation)
	}
	l.runFrameCallbacks()
	if strings.Join(ran, ",") != "a,next" {
		t.Fatal(ran)
	}
}
func TestAnimationSharedBudgetDefersUnstartedCallbacks(t *testing.T) {
	p, _ := profile.Builtin("browse")
	p.FrameBudgetMillis = 1
	l := frameFixture(t, p)
	h := (*host)(l)
	ran := 0
	// Host time also consumes the batch's wall-time budget. The second callback
	// must wait, even though the individual callback slice is much longer.
	h.RequestFrame(func(goja.Value, ...goja.Value) (goja.Value, error) { time.Sleep(5 * time.Millisecond); return nil, nil })
	h.RequestFrame(func(goja.Value, ...goja.Value) (goja.Value, error) { ran++; return nil, nil })
	l.runFrameCallbacks()
	if ran != 0 || len(l.page.animation) != 1 {
		t.Fatal("batch budget ignored")
	}
	l.runFrameCallbacks()
	if ran != 1 {
		t.Fatal("deferred callback lost")
	}
}
