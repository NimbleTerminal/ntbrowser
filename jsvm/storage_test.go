// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/sessionstorage"
	"strings"
	"testing"
)

func storageSandbox(t *testing.T, limit int) *Sandbox {
	t.Helper()
	tree, _ := dom.Parse(strings.NewReader(""), dom.Limits{})
	s := New(Config{SessionStorage: sessionstorage.New(limit, 0).Open("https://example.test/")}, tree, &nopHost{})
	t.Cleanup(s.Close)
	return s
}
func TestStorageInterfaceAndNamedProperties(t *testing.T) {
	s := storageSandbox(t, 0)
	imageEval(t, s, `let st=sessionStorage;st.setItem("a",1);st.b="two";st[3]=null;`)
	for _, tc := range []struct{ src, want string }{
		{`[st.length,st.getItem("a"),st.b,st[3],st.key(0),st.key(99),st.getItem("missing"),typeof st.missing,st===window.sessionStorage,st instanceof Storage].join("|")`, `3|1|two|null|a|||undefined|true|true`},
		{`Object.keys(st).sort().join()`, `3,a,b`},
		{`Object.prototype.toString.call(st)`, `[object Storage]`},
	} {
		if got := evalString(t, s, tc.src); got != tc.want {
			t.Fatalf("%s: %q", tc.src, got)
		}
	}
	imageEval(t, s, `st.setItem("getItem","saved");st.setItem("length","saved-length");st.setItem("__proto__","data");`)
	if got := evalString(t, s, `[typeof st.getItem,st.getItem("getItem"),st.length,st.getItem("__proto__"),Object.keys(st).sort().join()].join("|")`); got != "function|saved|6|data|3,a,b" {
		t.Fatal(got)
	}
	imageEval(t, s, `delete st.b;st.removeItem("a");st.clear()`)
	if evalString(t, s, `st.length`) != "0" {
		t.Fatal("clear failed")
	}
}
func TestStorageQuotaAtomicAndUTF16(t *testing.T) {
	s := storageSandbox(t, 16)
	imageEval(t, s, `sessionStorage.setItem("a","1234567");let quota=false;try{sessionStorage.setItem("a","12345678")}catch(e){quota=e.name==="QuotaExceededError"}`)
	if got := evalString(t, s, `[quota,sessionStorage.getItem("a")].join()`); got != "true,1234567" {
		t.Fatal(got)
	}
	imageEval(t, s, `sessionStorage.clear();sessionStorage.setItem("\ud800","\udfff")`)
	if got := evalString(t, s, `[sessionStorage.key(0).charCodeAt(0),sessionStorage.getItem("\ud800").charCodeAt(0)].join()`); got != "55296,57343" {
		t.Fatal(got)
	}
	if got := evalString(t, s, `(()=>{try{sessionStorage.setItem(Symbol(),"x")}catch(e){return e.name}})()`); got != "TypeError" {
		t.Fatal(got)
	}
}
func TestStorageClosedAndIllegalReceiver(t *testing.T) {
	s := dynamicSandbox(t, false)
	if got := evalString(t, s, `(()=>{try{return sessionStorage}catch(e){return e.name}})()`); got != "SecurityError" {
		t.Fatal(got)
	}
	s = storageSandbox(t, 0)
	for _, src := range []string{`Storage.prototype.getItem.call({},"x")`, `new Storage()`, `sessionStorage.setItem("x")`} {
		if err := s.Eval("bad.js", src); err == nil {
			t.Fatal(src)
		}
	}
}
