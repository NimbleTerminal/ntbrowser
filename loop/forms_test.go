// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"image"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml/document"
)

// formLoop loads page and returns the loop, its sink and the first frame.
func formLoop(t *testing.T, page string) (*Loop, chanSink, FrameMsg) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: testProfile, Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(page, "https://example.test/form", nil)
	f := waitFor(t, sink, func(m tea.Msg) bool { f, ok := m.(FrameMsg); return ok && f.Err == nil && f.Result != nil })
	return l, sink, f.(FrameMsg)
}

func waitFor(t *testing.T, sink chanSink, match func(tea.Msg) bool) tea.Msg {
	t.Helper()
	deadline := time.After(patience)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for message")
		case m := <-sink:
			if match(m) {
				return m
			}
		}
	}
}

func waitFocus(t *testing.T, sink chanSink) FocusMsg {
	t.Helper()
	return waitFor(t, sink, func(m tea.Msg) bool { _, ok := m.(FocusMsg); return ok }).(FocusMsg)
}

func boxOf(t *testing.T, r *document.Result, anchor string) image.Rectangle {
	t.Helper()
	for _, b := range r.Boxes {
		if b.Anchor == anchor && !b.Bounds.Empty() {
			return b.Bounds
		}
	}
	t.Fatalf("no box for %s", anchor)
	return image.Rectangle{}
}

func centre(r image.Rectangle) image.Rectangle {
	c := r.Min.Add(r.Size().Div(2))
	return image.Rect(c.X, c.Y, c.X+1, c.Y+1)
}

func TestFocusNextOrder(t *testing.T) {
	l, sink, _ := formLoop(t, `<p><a href="/x" id=l>link</a> <input id=t2 tabindex=2> <input id=i> <button disabled id=d>b</button></p><div hidden><input id=h></div><p><input id=n tabindex=-1></p>`)
	var got []string
	for range 4 {
		l.FocusNext(false)
		f := waitFocus(t, sink)
		if !f.User {
			t.Fatalf("FocusNext not a user focus: %+v", f)
		}
		got = append(got, f.Source)
	}
	if want := []string{"t2", "l", "i", "t2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	l.FocusNext(true)
	if f := waitFocus(t, sink); f.Source != "i" {
		t.Fatalf("backward from t2: %s", f.Source)
	}
}

func TestClickFocusesControl(t *testing.T) {
	l, sink, frame := formLoop(t, `<p><input id=q placeholder=Search></p>`)
	l.ClickAt(centre(boxOf(t, frame.Result, "q")))
	f := waitFocus(t, sink)
	if f.Source != "q" || !f.User || f.Control.Kind != "text" || f.Control.Label != "Search" {
		t.Fatalf("%+v", f)
	}
}

func TestScriptFocusIsNotUser(t *testing.T) {
	// Not formLoop: the first focus happens while the page loads, before any frame.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: testProfile, Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(`<input id=a><input id=b><script>
		document.getElementById("a").focus();
		setTimeout(() => document.getElementById("b").focus(), 150);
		setTimeout(() => document.getElementById("b").blur(), 300);
	</script>`, "https://example.test/", nil)
	for _, want := range []string{"a", "b", ""} {
		f := waitFocus(t, sink)
		if f.Source != want || f.User {
			t.Fatalf("want script focus %q, got %+v", want, f)
		}
	}
}

func TestAutofocusIsScriptFocus(t *testing.T) {
	_, sink, _ := formLoop(t, `<p><input id=a autofocus></p>`)
	if f := waitFocus(t, sink); f.Source != "a" || f.User {
		t.Fatalf("%+v", f)
	}
}

func TestActivateCheckboxAndRadio(t *testing.T) {
	l, sink, _ := formLoop(t, `<p><input type=checkbox id=c><input type=checkbox id=p><input type=radio name=r id=r1 checked><input type=radio name=r id=r2></p><script>
		const $ = id => document.getElementById(id);
		$("c").addEventListener("change", e => console.log("c change " + e.target.checked));
		$("p").addEventListener("click", e => { e.preventDefault(); setTimeout(() => console.log("p after " + $("p").checked)) });
		$("r2").addEventListener("change", () => console.log("r2 change, r1 " + $("r1").checked));
	</script>`)
	l.FocusNext(false)
	waitFocus(t, sink)
	l.Activate()
	waitLog(t, sink, "c change true")
	l.FocusNext(false)
	waitFocus(t, sink)
	l.Activate()
	waitLog(t, sink, "p after false")
	l.FocusNext(false) // r1
	waitFocus(t, sink)
	l.FocusNext(false) // r2
	waitFocus(t, sink)
	l.Activate()
	waitLog(t, sink, "r2 change, r1 false")
}

func TestSetValueDispatchesInputAndChange(t *testing.T) {
	l, sink, _ := formLoop(t, `<p><input id=i></p><script>
		const i = document.getElementById("i");
		i.addEventListener("input", () => console.log("input " + i.value));
		i.addEventListener("change", () => console.log("change " + i.value));
	</script>`)
	l.FocusNext(false)
	waitFocus(t, sink)
	l.SetValue("i", "ab", false)
	waitLog(t, sink, "input ab")
	l.SetValue("i", "abc", true)
	waitLog(t, sink, "input abc")
	waitLog(t, sink, "change abc")
}

func TestChooseSelect(t *testing.T) {
	l, sink, _ := formLoop(t, `<p><select id=s><optgroup label=G><option>a</option></optgroup><option disabled>d</option><option>b</option></select>
		<select id=m multiple><option>x</option><option>y</option></select></p><script>
		document.getElementById("s").addEventListener("change", e => console.log("s " + e.target.value));
		document.getElementById("m").addEventListener("change", e => console.log("m " + [...e.target.options].map(o => o.selected).join()));
	</script>`)
	l.FocusNext(false)
	f := waitFocus(t, sink)
	if f.Control.Kind != "select" || !reflect.DeepEqual(f.Control.Options, []string{"a", "b"}) || !reflect.DeepEqual(f.Control.OptionIndex, []int{0, 2}) {
		t.Fatalf("%+v", f.Control)
	}
	l.Choose("s", 2, false)
	waitLog(t, sink, "s b")
	l.FocusNext(false)
	if f := waitFocus(t, sink); !f.Control.Multiple {
		t.Fatalf("%+v", f.Control)
	}
	l.Choose("m", 1, true)
	waitLog(t, sink, "m false,true")
}

func TestFocusedControlRemovedClearsFocus(t *testing.T) {
	l, sink, _ := formLoop(t, `<p><input id=i></p><script>setTimeout(() => document.getElementById("i").remove(), 300)</script>`)
	l.FocusNext(false)
	if f := waitFocus(t, sink); f.Source != "i" {
		t.Fatalf("%+v", f)
	}
	if f := waitFocus(t, sink); f.Source != "" {
		t.Fatalf("removal did not clear focus: %+v", f)
	}
}

func TestScriptValueWriteSendsValueMsg(t *testing.T) {
	l, sink, _ := formLoop(t, `<p><input id=i></p><script>setTimeout(() => { document.getElementById("i").value = "zz" }, 300)</script>`)
	l.FocusNext(false)
	waitFocus(t, sink)
	m := waitFor(t, sink, func(m tea.Msg) bool { _, ok := m.(ValueMsg); return ok }).(ValueMsg)
	if m.Source != "i" || m.Value != "zz" {
		t.Fatalf("%+v", m)
	}
}

func TestLabelActivatesControl(t *testing.T) {
	l, sink, frame := formLoop(t, `<p><label for=c id=lab>Check me please</label> <input type=checkbox id=c></p><script>
		document.getElementById("c").addEventListener("change", e => console.log("c " + e.target.checked));
	</script>`)
	var area image.Rectangle
	for _, run := range frame.Result.TextIndex().Runs {
		if run.SourceID == "lab" && strings.TrimSpace(frame.Result.TextIndex().Text[run.Start:run.End]) != "" {
			area = centre(run.Bounds)
			break
		}
	}
	if area.Empty() {
		t.Fatal("no text run for the label")
	}
	l.ClickAt(area)
	if f := waitFocus(t, sink); f.Source != "c" || !f.User {
		t.Fatalf("%+v", f)
	}
	waitLog(t, sink, "c true")
}

func waitNavigate(t *testing.T, sink chanSink) NavigateMsg {
	t.Helper()
	return waitFor(t, sink, func(m tea.Msg) bool { _, ok := m.(NavigateMsg); return ok }).(NavigateMsg)
}

// noMessage fails if a message matching match arrives within d.
func noMessage(t *testing.T, sink chanSink, d time.Duration, match func(tea.Msg) bool) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case <-deadline:
			return
		case m := <-sink:
			if match(m) {
				t.Fatalf("unexpected %#v", m)
			}
		}
	}
}

func isNavigate(m tea.Msg) bool { _, ok := m.(NavigateMsg); return ok }

func TestImplicitSubmitWithButton(t *testing.T) {
	l, sink, _ := formLoop(t, `<form action="/search"><input id=q name=q><button name=b value=1>Go</button></form>`)
	l.SetValue("q", "hello world", true)
	l.ImplicitSubmit("q")
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/search?q=hello+world&b=1" || !nav.Form {
		t.Fatalf("%+v", nav)
	}
}

func TestImplicitSubmitRules(t *testing.T) {
	l, sink, _ := formLoop(t, `<form action="/one"><input id=a name=a value=1></form><form action="/two"><input id=b name=b><input id=c name=c></form>`)
	l.ImplicitSubmit("a")
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/one?a=1" {
		t.Fatalf("%+v", nav)
	}
	l.ImplicitSubmit("b")
	noMessage(t, sink, 300*time.Millisecond, isNavigate)
}

func TestEnterOutsideFormDoesNothing(t *testing.T) {
	l, sink, _ := formLoop(t, `<p><input id=a name=a value=1></p>`)
	l.ImplicitSubmit("a")
	noMessage(t, sink, 300*time.Millisecond, func(m tea.Msg) bool {
		_, log := m.(LogMsg)
		return isNavigate(m) || log
	})
}

func TestSubmitEventPrevented(t *testing.T) {
	l, sink, _ := formLoop(t, `<form id=f action="/s"><input id=q name=q></form><script>
		document.getElementById("f").addEventListener("submit", e => { e.preventDefault(); console.log("handled") });
	</script>`)
	l.ImplicitSubmit("q")
	waitLog(t, sink, "handled")
	noMessage(t, sink, 300*time.Millisecond, isNavigate)
}

func TestPostRefused(t *testing.T) {
	l, sink, _ := formLoop(t, `<form id=f method=post action="/login"><input id=q name=q value=secret></form><script>
		document.getElementById("f").addEventListener("submit", () => console.log("submit ran"));
	</script>`)
	l.ImplicitSubmit("q")
	waitLog(t, sink, "submit ran")
	m := waitFor(t, sink, func(m tea.Msg) bool { _, ok := m.(LogMsg); return ok }).(LogMsg)
	if m.Line != "POST forms are not supported yet" || strings.Contains(m.Line, "secret") {
		t.Fatalf("%+v", m)
	}
	noMessage(t, sink, 300*time.Millisecond, isNavigate)
}

func TestPasswordGetRefused(t *testing.T) {
	l, sink, _ := formLoop(t, `<form action="/a"><input type=password id=p name=p><button id=b>go</button></form>`)
	l.SetValue("p", "hunter2", true)
	l.ImplicitSubmit("p")
	m := waitFor(t, sink, func(m tea.Msg) bool { _, ok := m.(LogMsg); return ok }).(LogMsg)
	if !strings.Contains(m.Line, "password") || strings.Contains(m.Line, "hunter2") {
		t.Fatalf("%+v", m)
	}
	noMessage(t, sink, 300*time.Millisecond, isNavigate)
	l.SetValue("p", "", true)
	l.ImplicitSubmit("p")
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/a?p=" {
		t.Fatalf("%+v", nav)
	}
}

func TestFormActionAndQueryReplacement(t *testing.T) {
	l, sink, _ := formLoop(t, `<form action="/s?old=1#f"><input id=q name=q value=x><button id=b1>a</button><button id=b2 formaction="/other">b</button></form>
		<form action="javascript:alert(1)"><input id=j name=j></form><form action="data:text/html,hi"><input id=d name=d></form>`)
	l.ImplicitSubmit("q")
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/s?q=x" {
		t.Fatalf("%+v", nav)
	}
	l.FocusNext(false) // q
	waitFocus(t, sink)
	l.FocusNext(false) // b1
	waitFocus(t, sink)
	l.FocusNext(false) // b2
	waitFocus(t, sink)
	l.Activate()
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/other?q=x" {
		t.Fatalf("formaction: %+v", nav)
	}
	l.ImplicitSubmit("j")
	l.ImplicitSubmit("d")
	noMessage(t, sink, 300*time.Millisecond, isNavigate)
}

func TestSubmitEncodesUTF8(t *testing.T) {
	l, sink, _ := formLoop(t, `<form action="/s"><input id=q name=q></form>`)
	l.SetValue("q", "漢 é", true)
	l.ImplicitSubmit("q")
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/s?q=%E6%BC%A2+%C3%A9" {
		t.Fatalf("%+v", nav)
	}
}

func TestScriptSubmitAndRequestSubmit(t *testing.T) {
	_, sink, _ := formLoop(t, `<form id=f action="/s"><input name=q value=1></form><script>
		const f = document.getElementById("f");
		f.addEventListener("submit", e => { console.log("submit event"); e.preventDefault() });
		setTimeout(() => f.requestSubmit(), 50);
		setTimeout(() => f.submit(), 300);
	</script>`)
	waitLog(t, sink, "submit event") // requestSubmit fired it and was prevented
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/s?q=1" {
		t.Fatalf("%+v", nav)
	}
}

func TestResetRestoresDefaults(t *testing.T) {
	l, sink, _ := formLoop(t, `<form id=f><input id=q name=q value=d><input type=checkbox id=c checked><button type=reset id=r>reset</button></form><script>
		document.getElementById("f").addEventListener("reset", () => setTimeout(() =>
			console.log("after reset " + document.getElementById("q").value + " " + document.getElementById("c").checked)));
	</script>`)
	l.SetValue("q", "changed", true)
	l.FocusNext(false) // q
	waitFocus(t, sink)
	l.FocusNext(false) // c
	waitFocus(t, sink)
	l.Activate()       // uncheck
	l.FocusNext(false) // r
	waitFocus(t, sink)
	l.Activate()
	waitLog(t, sink, "after reset d true")
}

func TestDialogMethodClosesDialog(t *testing.T) {
	l, sink, _ := formLoop(t, `<dialog id=dlg open><form method=dialog><input id=q name=q><button>ok</button></form></dialog><script>
		setTimeout(() => {}, 0);
	</script>`)
	l.ImplicitSubmit("q")
	f := waitFor(t, sink, func(m tea.Msg) bool {
		f, ok := m.(FrameMsg)
		if !ok || f.Result == nil {
			return false
		}
		text, _ := f.Result.TTY(context.Background(), 60)
		return !strings.Contains(text, "ok")
	}).(FrameMsg)
	_ = f
	noMessage(t, sink, 200*time.Millisecond, isNavigate)
}

// Review #1: a submit or reset listener that re-requests submission (or
// reset) must not loop forever; the loop keeps serving the reader.
func TestReRequestInListenerDoesNotHang(t *testing.T) {
	l, sink, _ := formLoop(t, `<form id=f action="/s"><input id=q name=q></form><form id=g><input id=r name=r></form><script>
		const f = document.getElementById("f"), g = document.getElementById("g");
		f.addEventListener("submit", e => { e.preventDefault(); console.log("submit"); f.requestSubmit() });
		g.addEventListener("reset", () => { console.log("reset"); g.reset() });
		setTimeout(() => f.requestSubmit(), 50);
		setTimeout(() => g.reset(), 100);
	</script>`)
	waitLog(t, sink, "submit")
	waitLog(t, sink, "reset")
	l.FocusNext(false)
	waitFocus(t, sink) // the loop is still alive
}

// Review #3: duplicate ids must not send typing or submission to the wrong
// field: each focusable element gets a frame-unique anchor.
func TestDuplicateIDsTargetTheFocusedField(t *testing.T) {
	l, sink, _ := formLoop(t, `<form action="/a"><input id=q name=q1></form><form action="/b"><input id=q name=q2></form>`)
	l.FocusNext(false)
	first := waitFocus(t, sink)
	l.FocusNext(false)
	second := waitFocus(t, sink)
	if first.Source == second.Source {
		t.Fatalf("both fields report anchor %q", first.Source)
	}
	l.SetValue(second.Source, "x", true)
	l.ImplicitSubmit(second.Source)
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/b?q2=x" {
		t.Fatalf("%+v", nav)
	}

	l2, sink2, _ := formLoop(t, `<form action="/a" style="display:none"><input id=q name=q1></form><form action="/b"><input id=q name=q2></form>`)
	l2.FocusNext(false)
	f := waitFocus(t, sink2)
	l2.SetValue(f.Source, "y", true)
	l2.ImplicitSubmit(f.Source)
	if nav := waitNavigate(t, sink2); nav.Href != "https://example.test/b?q2=y" {
		t.Fatalf("hidden duplicate first: %+v", nav)
	}
}

// Review #4: leaving an edited field any way (a click elsewhere, script
// focus) fires change, and re-focusing the same field does not move the
// change baseline.
func TestChangeFiresOnBlur(t *testing.T) {
	l, sink, frame := formLoop(t, `<p><input id=i><input id=j></p><script>
		document.getElementById("i").addEventListener("change", e => console.log("change " + e.target.value));
	</script>`)
	l.FocusNext(false)
	waitFocus(t, sink)
	l.SetValue("i", "ab", false)
	l.ClickAt(centre(boxOf(t, frame.Result, "i"))) // same field again: no change, baseline kept
	waitFocus(t, sink)
	l.ClickAt(centre(boxOf(t, frame.Result, "j")))
	waitLog(t, sink, "change ab")
}

func videoProfileLoop(t *testing.T, video bool, page string) (*Loop, chanSink, FrameMsg) {
	t.Helper()
	p := testProfile
	if video {
		p = p.WithVideo()
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: p, Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(page, "https://example.test/dir/page", nil)
	f := waitFor(t, sink, func(m tea.Msg) bool { f, ok := m.(FrameMsg); return ok && f.Err == nil && f.Result != nil })
	return l, sink, f.(FrameMsg)
}

const videoPage = `<p><video id=v width=100 height=50 src="clip.mp4" autoplay></video></p><script>console.log("play " + typeof document.getElementById("v").play)</script>`

func TestVideoElementFocusableOnlyWhenOn(t *testing.T) {
	l, sink, _ := videoProfileLoop(t, true, videoPage)
	l.FocusNext(false)
	f := waitFocus(t, sink)
	if f.Source != "v" || f.Control.Kind != "video" {
		t.Fatalf("%+v", f)
	}
	l.Activate()
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/dir/clip.mp4" {
		t.Fatalf("%+v", nav)
	}
	l2, sink2, _ := videoProfileLoop(t, false, videoPage)
	l2.FocusNext(false)
	noMessage(t, sink2, 200*time.Millisecond, func(m tea.Msg) bool { _, ok := m.(FocusMsg); return ok })
}

func TestVideoSourceChild(t *testing.T) {
	l, sink, _ := videoProfileLoop(t, true, `<p><video id=v width=100 height=50><source src="a.ogg" type="audio/ogg"><source src="b.webm" type="video/webm"></video></p>`)
	l.FocusNext(false)
	waitFocus(t, sink)
	l.Activate()
	if nav := waitNavigate(t, sink); nav.Href != "https://example.test/dir/b.webm" {
		t.Fatalf("%+v", nav)
	}
}

func TestAutoplayIgnored(t *testing.T) {
	_, sink, _ := videoProfileLoop(t, true, videoPage)
	noMessage(t, sink, 300*time.Millisecond, isNavigate)
}

func TestScriptCannotStartVideo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: testProfile.WithVideo(), Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(videoPage, "https://example.test/", nil)
	waitLog(t, sink, "play undefined")
}
