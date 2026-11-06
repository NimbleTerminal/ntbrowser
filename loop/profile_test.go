// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/profile"
)

func profileLoop(t *testing.T, p profile.Profile, page string) (*Loop, chanSink) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: p, Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(page, "https://example.test/", nil)
	return l, sink
}

func TestScriptsOffRunsNothing(t *testing.T) {
	p, _ := profile.Builtin("locked")
	_, sink := profileLoop(t, p, `<p id=p>static</p><script>console.log("ran")</script>`)
	f := waitFor(t, sink, func(m tea.Msg) bool {
		if lm, ok := m.(LogMsg); ok && lm.Line == "ran" {
			t.Fatal("script ran under scripts=off")
		}
		fr, ok := m.(FrameMsg)
		return ok && fr.Result != nil
	})
	if f == nil {
		t.Fatal("no frame")
	}
}

func TestFetchOffRejects(t *testing.T) {
	p, _ := profile.Builtin("browse")
	p.Fetch = profile.FetchOff
	_, sink := profileLoop(t, p, `<script>fetch("/x").then(() => console.log("fetched")).catch(() => console.log("refused"))</script>`)
	waitLog(t, sink, "refused")
}

func TestClockResolution(t *testing.T) {
	p, _ := profile.Builtin("browse")
	p.ClockMillis = 1000
	_, sink := profileLoop(t, p, `<script>console.log("mod " + (Date.now() % 1000))</script>`)
	waitLog(t, sink, "mod 0")
}

func TestPageCannotSeeProfile(t *testing.T) {
	p, _ := profile.Builtin("browse")
	_, sink := profileLoop(t, p, `<script>
		const names = Object.getOwnPropertyNames(globalThis).filter(n => /profile|^gate|sandbox/i.test(n));
		console.log("names=" + names.join());
	</script>`)
	m := waitFor(t, sink, func(m tea.Msg) bool { _, ok := m.(LogMsg); return ok })
	if lm := m.(LogMsg); lm.Line != "names=" {
		t.Fatalf("%+v", lm)
	}
}

func TestPerformanceAndAnimationFramesRespectProfileClock(t *testing.T) {
	p, _ := profile.Builtin("browse")
	p.ClockMillis = 1000
	_, sink := profileLoop(t, p, `<script>
 if(performance.timeOrigin%1000!==0||performance.now()%1000!==0)throw Error("fine performance clock");
 requestAnimationFrame(t=>{if(t%1000!==0||performance.now()<t)throw Error("fine frame clock");console.log("coarse clocks")});
 </script>`)
	waitLog(t, sink, "coarse clocks")
}

func TestDynamicCodeRespectsProfileAndFetchGate(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[on], func(t *testing.T) {
			p, _ := profile.Builtin("browse")
			p.Fetch = profile.FetchOff
			if on {
				p.DynamicCode = profile.DynamicOn
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sink := make(chanSink, 256)
			// The loop must override even a conflicting low-level VM configuration.
			l := New(ctx, Config{Profile: p, Width: 400, Script: jsvm.Config{AllowDynamicCode: !on}}, sink)
			go l.Run()
			l.LoadPage(`<script>try { eval('fetch("/x").then(()=>console.log("escaped"),()=>console.log("fetch refused"))'); } catch(e) { console.log(e.name); }</script>`, "https://example.test/", nil)
			want := "EvalError"
			if on {
				want = "fetch refused"
			}
			waitLog(t, sink, want)
		})
	}
}
