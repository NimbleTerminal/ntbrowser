// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestUncaughtErrorLocation(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	err := s.Eval("https://example.test/search#inline-2", "\nimage.src = 'x';")
	if err == nil || !strings.Contains(err.Error(), "ReferenceError: image is not defined at https://example.test/search#inline-2:2:1") {
		t.Fatalf("missing script location: %v", err)
	}
	// A callback executes in a separate entry but must name its defining script.
	if err := s.Eval("callback.js", "function callback() {\nimage.src = 'x';\n}"); err != nil {
		t.Fatal(err)
	}
	fn, ok := goja.AssertFunction(s.vm.Get("callback"))
	if !ok {
		t.Fatal("missing callback")
	}
	err = s.Call(fn, goja.Undefined())
	if err == nil || !strings.Contains(err.Error(), "at callback.js:2:1") {
		t.Fatalf("callback location: %v", err)
	}
}

func TestUncaughtErrorLocationSanitizedAndBounded(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	for _, name := range []string{"page\x1b\x07\n\u009b\u2028.js", strings.Repeat("x", 5000)} {
		err := s.Eval(name, `throw new Error("boom")`)
		if err == nil {
			t.Fatal("expected exception")
		}
		msg := strings.TrimPrefix(err.Error(), "ntbrowser/jsvm: uncaught ")
		if strings.ContainsAny(msg, "\x1b\x07\n\u009b\u2028") || len(msg) > 4096+len("…") {
			t.Fatalf("unsafe or oversized diagnostic: %q", msg)
		}
	}
}
