package jsvm

import (
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/dom"
)

func formSandbox(t *testing.T, src string) (*Sandbox, *dom.Tree, *nopHost) {
	t.Helper()
	tree, err := dom.Parse(strings.NewReader(src), dom.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	h := &nopHost{}
	return New(Config{}, tree, h), tree, h
}

func evalString(t *testing.T, s *Sandbox, src string) string {
	t.Helper()
	if err := s.Eval("t.js", "globalThis.__r = String("+src+")"); err != nil {
		t.Fatal(err)
	}
	return s.vm.Get("__r").String()
}

func TestValueBindingVersusAttribute(t *testing.T) {
	s, tree, _ := formSandbox(t, `<input id=i value=a><textarea id=t>x</textarea>`)
	if got := evalString(t, s, `(() => { const i = document.getElementById("i"); i.value = "b"; return [i.value, i.getAttribute("value"), i.defaultValue].join() })()`); got != "b,a,a" {
		t.Fatal(got)
	}
	if tree.ByID("i").Value() != "b" {
		t.Fatal("tree state")
	}
	if got := evalString(t, s, `(() => { const t = document.getElementById("t"); t.value = "y"; return [t.value, t.defaultValue, t.type].join() })()`); got != "y,x,textarea" {
		t.Fatal(got)
	}
	if got := evalString(t, s, `document.createElement("div").value`); got != "undefined" {
		t.Fatal(got)
	}
}

func TestCheckedAndRadioGroupInJS(t *testing.T) {
	s, _, _ := formSandbox(t, `<form><input type=radio name=r id=r1 checked><input type=radio name=r id=r2></form><form><input type=radio name=r id=r3 checked></form><input type=checkbox id=c>`)
	got := evalString(t, s, `(() => {
		const $ = id => document.getElementById(id);
		$("r2").checked = true; $("c").checked = true;
		return [$("r1").checked, $("r2").checked, $("r3").checked, $("c").checked, $("c").defaultChecked].join() })()`)
	if got != "false,true,true,true,false" {
		t.Fatal(got)
	}
}

func TestSelectBindings(t *testing.T) {
	s, _, _ := formSandbox(t, `<select id=s><option>a</option><option value=bv>b</option><option>c</option></select>`)
	got := evalString(t, s, `(() => {
		const s = document.getElementById("s");
		const before = [s.selectedIndex, s.value, s.options.length, s.options[0].selected, s.type].join();
		s.options[1].selected = true;
		const mid = [s.selectedIndex, s.value, s.options[0].selected, s.options[1].text].join();
		s.selectedIndex = 2;
		s.value = "bv";
		return before + "|" + mid + "|" + [s.selectedIndex, s.value].join() })()`)
	if got != "0,a,3,true,select-one|1,bv,false,b|1,bv" {
		t.Fatal(got)
	}
}

func TestFormElementsAndNamedItem(t *testing.T) {
	s, _, _ := formSandbox(t, `<form id=f><input name=q value=1><select name=s></select></form><input form=f name=x>`)
	got := evalString(t, s, `(() => { const f = document.getElementById("f"); return [f.elements.length, f.elements.namedItem("x").name, f.elements.namedItem("nope") === null, document.querySelector("input").form === f].join() })()`)
	if got != "3,x,true,true" {
		t.Fatal(got)
	}
}

func TestSubmitHooks(t *testing.T) {
	s, tree, _ := formSandbox(t, `<form id=f><button id=b>go</button></form><form id=g><button id=other>x</button></form>`)
	type call struct {
		form, submitter dom.NodeID
		fire            bool
	}
	var calls []call
	var resets []dom.NodeID
	s.SetFormHooks(FormHooks{
		Submit: func(form, submitter dom.NodeID, fire bool) { calls = append(calls, call{form, submitter, fire}) },
		Reset:  func(form dom.NodeID) { resets = append(resets, form) },
	})
	f, b := tree.ByID("f").ID(), tree.ByID("b").ID()
	if err := s.Eval("t.js", `const f = document.getElementById("f"); f.submit(); f.requestSubmit(document.getElementById("b")); f.requestSubmit(); f.reset();`); err != nil {
		t.Fatal(err)
	}
	want := []call{{f, 0, false}, {f, b, true}, {f, 0, true}}
	if len(calls) != 3 || calls[0] != want[0] || calls[1] != want[1] || calls[2] != want[2] || len(resets) != 1 || resets[0] != f {
		t.Fatalf("calls %+v resets %v", calls, resets)
	}
	if got := evalString(t, s, `(() => { try { f.requestSubmit(document.getElementById("other")); return "no throw" } catch (e) { return e.name } })()`); got != "TypeError" {
		t.Fatal(got)
	}
}

func TestFocusEventsBubbling(t *testing.T) {
	s, tree, h := formSandbox(t, `<div id=d><input id=a><input id=b></div><span id=s>no</span>`)
	if err := s.Eval("t.js", `
		const $ = id => document.getElementById(id);
		$("d").addEventListener("focus", () => console.log("parent focus"));
		$("d").addEventListener("focusin", e => console.log("parent focusin " + e.target.id));
		$("a").addEventListener("blur", () => console.log("a blur"));
		$("d").addEventListener("focusout", e => console.log("parent focusout " + e.target.id));
		$("a").addEventListener("focus", () => console.log("a focus"));`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFocus(tree.ByID("a").ID()); err != nil {
		t.Fatal(err)
	}
	if err := s.Eval("t2.js", `document.getElementById("b").focus(); document.getElementById("s").focus(); console.log("active " + document.activeElement.id)`); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(h.lines, "|")
	want := "log: a focus|log: parent focusin a|log: a blur|log: parent focusout a|log: parent focusin b|log: active b"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// Listener-free focus changes never enter the VM: they work on a closed sandbox.
	s2, tree2, _ := formSandbox(t, `<input id=a>`)
	s2.Close()
	if err := s2.SetFocus(tree2.ByID("a").ID()); err != nil || s2.Active() != tree2.ByID("a").ID() {
		t.Fatalf("closed sandbox focus: %v %v", err, s2.Active())
	}
}

func TestValueChangedHook(t *testing.T) {
	s, tree, _ := formSandbox(t, `<input id=i>`)
	var changed []dom.NodeID
	s.SetFormHooks(FormHooks{ValueChanged: func(id dom.NodeID) { changed = append(changed, id) }})
	if err := s.Eval("t.js", `document.getElementById("i").value = "x"`); err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != tree.ByID("i").ID() {
		t.Fatalf("changed %v", changed)
	}
}

func TestNoOpValueWriteDoesNotNotify(t *testing.T) {
	s, _, _ := formSandbox(t, `<input id=i value=a>`)
	n := 0
	s.SetFormHooks(FormHooks{ValueChanged: func(dom.NodeID) { n++ }})
	if err := s.Eval("t.js", `const i = document.getElementById("i"); i.value = i.value; i.value = "b"; i.value = "b"`); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ValueChanged called %d times, want 1", n)
	}
}
