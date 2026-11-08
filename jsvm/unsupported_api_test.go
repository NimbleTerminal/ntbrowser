// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"errors"
	"strings"
	"testing"
)

// TestExcludedGlobalsReportAsUnsupportedAPI: a script that reaches for a
// global the design leaves out (XMLHttpRequest in Cloudflare's beacon) is
// reported as this browser's gap, with its location, not as a page error.
func TestExcludedGlobalsReportAsUnsupportedAPI(t *testing.T) {
	s := dynamicSandbox(t, false)
	err := s.Eval("https://example.test/beacon.js", "\nconst o = new XMLHttpRequest;")
	if !errors.Is(err, ErrUnsupportedAPI) || !strings.Contains(err.Error(), "script uses XMLHttpRequest, which this browser does not provide at https://example.test/beacon.js:2:15") {
		t.Fatalf("XMLHttpRequest: %v", err)
	}
	for _, src := range []string{"new WebSocket('wss://x')", "WebAssembly.instantiate(1)", "new Worker('w.js')"} {
		if err := s.Eval("x.js", src); !errors.Is(err, ErrUnsupportedAPI) {
			t.Fatalf("%s: %v", src, err)
		}
	}
	// Other ReferenceErrors, and non-native errors with the same text, stay
	// uncaught page errors.
	for _, src := range []string{"new URL('x')", "foo()", `throw new TypeError("XMLHttpRequest is not defined")`, `const e=new Error("XMLHttpRequest is not defined");throw e`} {
		err := s.Eval("x.js", src)
		if err == nil || errors.Is(err, ErrUnsupportedAPI) || !strings.Contains(err.Error(), "uncaught") {
			t.Fatalf("%s: %v", src, err)
		}
	}
}

// TestScriptElementSupportsRecognizedTypes: HTMLScriptElement.supports names
// the script types the sandbox accepts, so pages do not install module
// polyfills that cannot run here; module scripts themselves stay inert.
func TestScriptElementSupportsRecognizedTypes(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 assert(typeof HTMLScriptElement.supports==='function');
 for(const ok of ['classic','module','importmap']) assert(HTMLScriptElement.supports(ok)===true, ok);
 for(const no of ['speculationrules','Module',' module','text/javascript','', null, undefined]) assert(HTMLScriptElement.supports(no)===false, String(no));
 let n='';try{HTMLScriptElement.supports()}catch(e){n=e.name}assert(n==='TypeError');
 const s=document.createElement('script');assert(s.constructor.supports('module')&&s.supports===undefined);
 `)
}
