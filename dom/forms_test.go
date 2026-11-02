// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package dom

import (
	"errors"
	"strings"
	"testing"
)

func TestValueStateVersusAttribute(t *testing.T) {
	tr := mustParse(t, `<input id=i value=a>`, Limits{})
	n := tr.ByID("i")
	if err := tr.SetValue(n, "b"); err != nil {
		t.Fatal(err)
	}
	if n.Value() != "b" {
		t.Fatalf("Value %q", n.Value())
	}
	if v, _ := n.Attr("value"); v != "a" {
		t.Fatalf("attribute changed: %q", v)
	}
	out := tr.Serialize()
	if !strings.Contains(out, `value="b"`) || strings.Contains(out, `value="a"`) {
		t.Fatalf("serialized %s", out)
	}
	// An input without a value attribute gains one once edited.
	tr = mustParse(t, `<input id=i>`, Limits{})
	_ = tr.SetValue(tr.ByID("i"), "x")
	if !strings.Contains(tr.Serialize(), `value="x"`) {
		t.Fatal(tr.Serialize())
	}
}

func TestTextareaValueSerialized(t *testing.T) {
	tr := mustParse(t, `<textarea id=t>x</textarea>`, Limits{})
	n := tr.ByID("t")
	if n.Value() != "x" {
		t.Fatalf("default %q", n.Value())
	}
	if err := tr.SetValue(n, "y<z"); err != nil {
		t.Fatal(err)
	}
	if out := tr.Serialize(); !strings.Contains(out, ">y&lt;z</textarea>") || strings.Contains(out, ">x</textarea>") {
		t.Fatal(out)
	}
	if TextContent(n) != "x" {
		t.Fatal("children changed")
	}
}

func TestCheckedAndSelectedSerialized(t *testing.T) {
	tr := mustParse(t, `<input id=a type=checkbox><input id=b type=checkbox checked><select><option id=o1 selected>1</option><option id=o2>2</option></select>`, Limits{})
	a, b := tr.ByID("a"), tr.ByID("b")
	if a.Checked() || !b.Checked() {
		t.Fatal("defaults")
	}
	tr.SetChecked(a, true)
	tr.SetChecked(b, false)
	o1, o2 := tr.ByID("o1"), tr.ByID("o2")
	tr.SetSelected(o1, false)
	tr.SetSelected(o2, true)
	out := tr.Serialize()
	for _, want := range []string{`id="a" type="checkbox" checked=""`, `id="o2" selected=""`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	for _, bad := range []string{`id="b" type="checkbox" checked`, `id="o1" selected`} {
		if strings.Contains(out, bad) {
			t.Fatalf("stale %s in %s", bad, out)
		}
	}
	if _, ok := b.Attr("checked"); !ok {
		t.Fatal("default attribute removed")
	}
}

func TestResetControlRestoresDefaults(t *testing.T) {
	tr := mustParse(t, `<input id=i value=a><input id=c type=checkbox checked><select><option id=o selected>1</option><option id=p>2</option></select>`, Limits{})
	i, c, o, p := tr.ByID("i"), tr.ByID("c"), tr.ByID("o"), tr.ByID("p")
	_ = tr.SetValue(i, "zzz")
	tr.SetChecked(c, false)
	tr.SetSelected(o, false)
	tr.SetSelected(p, true)
	for _, n := range []*Node{i, c, o, p} {
		tr.ResetControl(n)
	}
	if i.Value() != "a" || !c.Checked() || !o.Selected() || p.Selected() {
		t.Fatalf("reset: %q %v %v %v", i.Value(), c.Checked(), o.Selected(), p.Selected())
	}
}

func TestMaxLengthCountsRunes(t *testing.T) {
	tr := mustParse(t, `<input id=i maxlength=3>`, Limits{})
	n := tr.ByID("i")
	if err := tr.SetValue(n, "漢字かな"); err != nil {
		t.Fatal(err)
	}
	if n.Value() != "漢字か" {
		t.Fatalf("Value %q", n.Value())
	}
}

func TestSetValueChargesBudget(t *testing.T) {
	tr := mustParse(t, `<input id=i>`, Limits{TotalBytes: 2000})
	n := tr.ByID("i")
	before := tr.Bytes()
	if err := tr.SetValue(n, "a&b"); err != nil {
		t.Fatal(err)
	}
	if tr.Bytes() <= before {
		t.Fatalf("not charged: %d -> %d", before, tr.Bytes())
	}
	if err := tr.SetValue(n, strings.Repeat("x", 4000)); !errors.Is(err, ErrLimit) {
		t.Fatalf("want ErrLimit, got %v", err)
	}
	if n.Value() != "a&b" {
		t.Fatalf("value changed on failure: %q", n.Value())
	}
	if err := tr.SetValue(n, strings.Repeat("y", DefaultLimits.TextBytes+1)); !errors.Is(err, ErrLimit) {
		t.Fatalf("TextBytes cap: %v", err)
	}
	if len(tr.Serialize()) > tr.Bytes() {
		t.Fatalf("accounting below output: %d > %d", len(tr.Serialize()), tr.Bytes())
	}
}

func TestFormOwner(t *testing.T) {
	tr := mustParse(t, `<form id=f1><input id=a></form><form id=f2></form><div id=d></div><form id=f3><input id=b form=f2><input id=c form=d></form><input id=e>`, Limits{})
	for id, want := range map[string]string{"a": "f1", "b": "f2", "c": "f3", "e": ""} {
		got := FormOwner(tr.ByID(id), tr)
		name := ""
		if got != nil {
			name, _ = got.Attr("id")
		}
		if name != want {
			t.Errorf("%s: owner %q, want %q", id, name, want)
		}
	}
}

func TestFocusableAndMintedIDs(t *testing.T) {
	tr := mustParse(t, `<a href="/x">l</a><a>no href</a><input class=t><button disabled>b</button><input type=hidden name=h><fieldset disabled><legend><input class=leg></legend><input class=fs></fieldset><span tabindex=0>s</span><select></select><textarea></textarea>`, Limits{})
	q := func(sel string) *Node {
		ns, _ := tr.Query(tr.Root(), sel, false)
		if len(ns) == 0 {
			t.Fatalf("no %s", sel)
		}
		return ns[0]
	}
	cases := map[string]bool{"a": true, ".t": true, "button": false, ".leg": true, ".fs": false, "span": true, "select": true, "textarea": true}
	for sel, want := range cases {
		if got := Focusable(q(sel)); got != want {
			t.Errorf("Focusable(%s) = %v, want %v", sel, got, want)
		}
	}
	anchors, _ := tr.Query(tr.Root(), "a", true)
	if Focusable(anchors[1]) {
		t.Error("<a> without href is not focusable")
	}
	hidden, _ := tr.Query(tr.Root(), "input", true)
	for _, n := range hidden {
		if v, _ := n.Attr("type"); v == "hidden" && Focusable(n) {
			t.Error("hidden input focusable")
		}
	}
	out := tr.Serialize()
	for _, sel := range []string{"a", ".t", "span", "select", "textarea"} {
		n := q(sel)
		id := syntheticID(n.ID())
		if !strings.Contains(out, `id="`+id+`"`) || tr.ByID(id) != n {
			t.Errorf("%s: no minted id %s", sel, id)
		}
	}
	if strings.Contains(out, `id="`+syntheticID(q("button").ID())+`"`) {
		t.Error("disabled button got a minted id")
	}
}

func TestFrameAnchorsAreUnique(t *testing.T) {
	tr := mustParse(t, `<input id=q class=a><input id=q class=b><div id=q class=c></div><input id="nt-h-1" class=d><input id="" class=e>`, Limits{})
	q := func(sel string) *Node { ns, _ := tr.Query(tr.Root(), sel, false); return ns[0] }
	a, b, c, d, e := q(".a"), q(".b"), q(".c"), q(".d"), q(".e")
	if AnchorID(tr, a) != "q" || AnchorID(tr, c) != "q" {
		t.Fatalf("first/unfocusable keep their id: %q %q", AnchorID(tr, a), AnchorID(tr, c))
	}
	for _, n := range []*Node{b, d, e} {
		id := AnchorID(tr, n)
		if id != syntheticID(n.ID()) || tr.ByID(id) != n {
			t.Errorf("%v: anchor %q not unique", n.ID(), id)
		}
		if !strings.Contains(tr.Serialize(), `id="`+id+`"`) {
			t.Errorf("frame lacks %q", id)
		}
	}
	if v, _ := b.Attr("id"); v != "q" {
		t.Fatal("tree attribute changed")
	}
}
