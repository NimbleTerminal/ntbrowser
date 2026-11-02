// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dop251/goja"

	"nimbleterminal.dev/ntbrowser/dom"
)

// TestSourceMapCommentNeverTouchesFilesystem shows the only observable of
// Goja's default source-map loader (os.ReadFile on a page-controlled
// file:// path; errors are swallowed, data only remaps stack positions) and
// proves the sandbox's compile path does not exhibit it.
//
// Control: compiling the same script with goja.Compile *does* read the map
// (the thrown error's frame is remapped to SECRET_SOURCE.js). If the control
// ever stops observing the remap, this test fails so it cannot silently turn
// into a no-op.
func TestSourceMapCommentNeverTouchesFilesystem(t *testing.T) {
	mapPath := filepath.Join(t.TempDir(), "page.js.map")
	const cols = "AAAA,CAAC,CAAC,CAAC,CAAC,CAAC,CAAC,CAAC,CAAC,CAAC"
	if err := os.WriteFile(mapPath, []byte(`{"version":3,"sources":["SECRET_SOURCE.js"],"names":[],"mappings":"`+strings.Repeat(cols+";", 3)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Goja resolves a bare absolute path relative to the script *name* (which
	// for ntbrowser is an https URL, making the scheme "unsupported"), but a
	// file:// URL is used verbatim: that is the live vector.
	src := "\nthrow new Error('boom')\n//# sourceMappingURL=file://" + mapPath + "\n"

	srcName := func(err error) string {
		var ex *goja.Exception
		if !errors.As(err, &ex) || len(ex.Stack()) == 0 {
			t.Fatalf("expected a JS exception with a stack, got %v", err)
		}
		return ex.Stack()[0].Position().Filename // Position() applies the map; SrcName() never does
	}

	// Control: the unhardened path reads the map file.
	prg, err := goja.Compile("page.js", src, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = goja.New().RunProgram(prg)
	if got := srcName(err); !strings.Contains(got, "SECRET_SOURCE.js") {
		t.Fatalf("control failed: goja.Compile did not apply the source map (src=%q); the oracle is broken", got)
	}

	// Sandbox path: same script, map must be ignored.
	tree, _ := dom.Parse(strings.NewReader(""), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	err = s.Eval("page.js", src)
	var ex *goja.Exception
	if !errors.As(err, &ex) {
		// Sandbox.Run wraps exceptions; unwrap by re-running through the raw path
		// of the sandbox's own program to inspect the frame.
		ast, perr := goja.Parse("page.js", src, sandboxParserOptions()...)
		if perr != nil {
			t.Fatal(perr)
		}
		prg, cerr := goja.CompileAST(ast, false)
		if cerr != nil {
			t.Fatal(cerr)
		}
		_, err = s.vm.RunProgram(prg)
	}
	if got := srcName(err); strings.Contains(got, "SECRET_SOURCE.js") {
		t.Fatalf("sandbox applied a page-supplied source map: src=%q", got)
	}
}
