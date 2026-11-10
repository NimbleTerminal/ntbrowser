// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/dom"
)

// closeKeeps are the Sandbox fields Close deliberately leaves alone. Anything
// else that can hold a reference must be released: a closed sandbox is kept
// alive by late callbacks (finalizers, timers, the loop's page record), and
// whatever it still points at stays alive with it.
var closeKeeps = map[string]string{
	"cfg":         "limits and host callbacks; Close drops the ones that reach the page's stores and loader",
	"host":        "late callbacks still report through it; it belongs to the loop",
	"tree":        "owned by the page, which outlives the sandbox",
	"wd":          "disarmed; its timer and mutex are reused by nothing",
	"collectedMu": "a mutex",
	"heapOver":    "a time",
}

// held lists the reference-holding fields of v that are not empty, by path.
func held(v reflect.Value, path string, out *[]string) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Chan, reflect.UnsafePointer:
		if !v.IsNil() {
			*out = append(*out, path)
		}
	case reflect.Map, reflect.Slice:
		if v.Len() != 0 {
			*out = append(*out, path)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			held(v.Field(i), path+"."+v.Type().Field(i).Name, out)
		}
	}
}

func sandboxHeld(s *Sandbox) []string {
	var out []string
	v := reflect.ValueOf(s).Elem()
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if _, keep := closeKeeps[name]; !keep {
			held(v.Field(i), name, &out)
		}
	}
	sort.Strings(out)
	return out
}

// Close must release every reference a sandbox holds. A feature that adds a
// field and forgets Close fails here; so does a field Close has always missed.
func TestCloseReleasesEveryReference(t *testing.T) {
	tree, err := dom.Parse(strings.NewReader(`<body><form id=f><input id=i></form><a id=a href="/x">x</a><img id=g><iframe id=fr></iframe></body>`), dom.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Config{}, tree, &nopHost{})
	s.SetFormHooks(FormHooks{ValueChanged: func(dom.NodeID) {}, Submit: func(dom.NodeID, dom.NodeID, bool) {}, Reset: func(dom.NodeID) {}})
	// Touch each feature so lazily built state exists before Close.
	if err := s.Eval("touch.js", `
		const a = document.getElementById('a'), i = document.getElementById('i');
		a.addEventListener('click', () => {}); addEventListener('x', () => {});
		new MutationObserver(() => {}).observe(document.body, {childList: true, subtree: true});
		document.body.appendChild(document.createElement('p'));
		const ch = new MessageChannel(); ch.port1.onmessage = () => {}; ch.port2.postMessage({k: 1});
		const ac = new AbortController(); ac.signal.addEventListener('abort', () => {});
		matchMedia('(min-width: 1px)').addEventListener('change', () => {});
		a.style.color = 'red'; a.relList.add('nofollow'); a.classList.add('c');
		new URL('https://example.test/?a=1').searchParams.get('a');
		document.getElementsByTagName('a'); new Event('y'); new CustomEvent('z');
		Promise.reject(new Error('unhandled')); i.value = 'v'; i.focus();
		globalThis.keep = [a, i, ch, ac];
	`); err != nil {
		t.Fatal(err)
	}
	before := sandboxHeld(s)
	if len(before) < 20 {
		t.Fatalf("the page set up only %d reference fields; the test would prove little: %v", len(before), before)
	}
	s.Close()
	if after := sandboxHeld(s); len(after) != 0 {
		t.Fatalf("Close left %d references:\n  %s", len(after), strings.Join(after, "\n  "))
	}
	// Every exemption must still name a field.
	typ := reflect.TypeOf(Sandbox{})
	for name := range closeKeeps {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("closeKeeps names %q, which Sandbox no longer has", name)
		}
	}
}
