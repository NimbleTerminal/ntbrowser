// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"errors"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/dom"
)

// TestEvalFlagsDynamicImportAsModuleSyntax covers the SvelteKit bootstrap
// shape: a classic inline script whose only unparseable construct is a
// dynamic import() call. Goja has no module loader, so the script is reported
// as unsupported module syntax rather than as a syntax error in the page.
func TestEvalFlagsDynamicImportAsModuleSyntax(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	src := "\n\t\t\t\t{\n\t\t\t\t\tconst element = document.currentScript;\n\t\t\t\t\tPromise.all([\n\t\t\t\t\t\timport(\"./_app/immutable/entry/start.js\"),\n\t\t\t\t\t\timport (\"./_app/immutable/entry/app.js\")\n\t\t\t\t\t]).then(([kit, app]) => kit.start(app, element));\n\t\t\t\t}\n"
	err := s.Eval("https://example.test/#inline-0", src)
	if !errors.Is(err, ErrModuleSyntax) {
		t.Fatalf("dynamic import not classified as module syntax: %v", err)
	}
	if strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("module syntax reported as a syntax error: %v", err)
	}

	// Genuine syntax errors, including other misuses of the import keyword,
	// keep their SyntaxError reports.
	for _, bad := range []string{"var import;", "import;", "foo(", "var enum;", "abc.import = 1; (", "import x from './a.js'", "import"} {
		err := s.Eval("https://example.test/#inline-1", bad)
		if err == nil || errors.Is(err, ErrModuleSyntax) || !strings.Contains(err.Error(), "SyntaxError") {
			t.Fatalf("%q: want a plain SyntaxError, got %v", bad, err)
		}
	}
	// An identifier that merely starts with "import" is ordinary code.
	if err := s.Eval("https://example.test/#inline-2", "var imports = 1; imports(2)"); err == nil || errors.Is(err, ErrModuleSyntax) || !strings.Contains(err.Error(), "TypeError") {
		t.Fatalf("identifier prefix misclassified: %v", err)
	}
}

// A parser failure for a language feature Goja has not implemented is an
// unsupported-syntax error, distinct from a syntax error in the page's code.
func TestUnsupportedSyntaxIsClassified(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body></body>"), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	t.Cleanup(s.Close)
	err := s.Eval("gen.js", "async function* g(){ yield 1 }")
	if !errors.Is(err, ErrUnsupportedSyntax) || !strings.Contains(err.Error(), "Async generators") {
		t.Fatalf("async generator: %v", err)
	}
	if err := s.Eval("bad.js", "function (){"); err == nil || errors.Is(err, ErrUnsupportedSyntax) {
		t.Fatalf("a real syntax error must stay an error: %v", err)
	}
}
