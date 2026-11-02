// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyHardenedCompilePath statically forbids every Goja entry point that
// parses source without our parser options. goja.Compile passes nil options
// (runtime.go: compile(name, src, strict, true, nil)); RunString/RunScript
// use the runtime's options but would make the rule harder to audit. The
// only allowed path is goja.Parse(name, src, parser.WithDisableSourceMaps)
// followed by goja.CompileAST, in Sandbox.Eval.
func TestOnlyHardenedCompilePath(t *testing.T) {
	forbidden := map[string]bool{"Compile": true, "MustCompile": true, "RunString": true, "RunScript": true,
		// NewGoError attaches the Go error as a reflected, writable `value` (spec §2 anti-goal).
		"NewGoError": true}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		parseCalls := 0
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if forbidden[sel.Sel.Name] {
				t.Errorf("%s: forbidden compile path %s", fset.Position(call.Pos()), sel.Sel.Name)
			}
			if sel.Sel.Name == "Parse" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "goja" {
					parseCalls++
					if len(call.Args) < 3 {
						t.Errorf("%s: goja.Parse without parser options", fset.Position(call.Pos()))
					}
				}
			}
			return true
		})
		_ = parseCalls
	}
}
