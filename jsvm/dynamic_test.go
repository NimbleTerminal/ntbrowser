// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
	"nimbleterminal.dev/ntbrowser/dom"
)

func dynamicSandbox(t *testing.T, on bool) *Sandbox {
	t.Helper()
	tree, err := dom.Parse(strings.NewReader(""), dom.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Config{AllowDynamicCode: on}, tree, &nopHost{})
	t.Cleanup(s.Close)
	return s
}

func TestDynamicCodeOffBlocksAllConstructors(t *testing.T) {
	expressions := []string{
		`eval("1")`, `(0,eval)("1")`, `window.eval.call(null,"1")`,
		`Function("return 1")`, `new Function("return 1")`,
		`(function(){}).constructor("return 1")`, `(()=>{}).constructor("return 1")`,
		`Object.getPrototypeOf(async function(){}).constructor("return 1")`,
		`Object.getPrototypeOf(function*(){}).constructor("yield 1")`,
		`Reflect.construct((function(){}).constructor,["return 1"])`,
		`Function.prototype.constructor("return 1")`,
		`Function.bind(null,"return 1")()`,
		`new (class extends Function {})("return 1")`,
	}
	for _, expr := range expressions {
		t.Run(expr, func(t *testing.T) {
			s := dynamicSandbox(t, false)
			got := evalString(t, s, `(()=>{try{`+expr+`;return "allowed"}catch(e){return [e instanceof EvalError,e.name,e.message].join("|")}})()`)
			if got != "true|EvalError|dynamic-code is off in this profile" {
				t.Fatal(got)
			}
		})
	}
}

func TestDynamicGateCannotBeReopenedByPage(t *testing.T) {
	s := dynamicSandbox(t, false)
	imageEval(t, s, `try{Object.defineProperty(Function.prototype,"constructor",{value:()=>42})}catch(e){};try{delete globalThis.eval}catch(e){};globalThis.eval=()=>42;`)
	if got := evalString(t, s, `(()=>{try{return eval("1")}catch(e){return e.name}})()`); got != "EvalError" {
		t.Fatal(got)
	}
	imageEval(t, s, `function ordinary(){return 2};function* gen(){yield 3};async function asyncLiteral(){return 4};`)
	if got := evalString(t, s, `[ordinary(),gen().next().value,ordinary instanceof Function].join()`); got != "2,3,true" {
		t.Fatal(got)
	}
}

func TestDynamicCodeOnPreservesEvalSemantics(t *testing.T) {
	s := dynamicSandbox(t, true)
	tests := []struct{ src, want string }{
		{`(()=>{let local=7;return eval("local+1")})()`, "8"},
		{`(()=>{let local=7;return (0,eval)("typeof local")})()`, "undefined"},
		{`(function(){eval("var added=9");return added})()`, "9"},
		{`(function(){"use strict";eval("var strictOnly=9");return typeof strictOnly})()`, "undefined"},
		{`eval(42)`, "42"},
		{`(()=>{const o={};return eval(o)===o})()`, "true"},
		{`new Function("a","return a+2")(3)`, "5"},
		{`(function*(){}).constructor("yield 6")().next().value`, "6"},
	}
	for _, tc := range tests {
		if got := evalString(t, s, tc.src); got != tc.want {
			t.Fatalf("%s: %s", tc.src, got)
		}
	}
	imageEval(t, s, `let asyncResult; (async function(){}).constructor("return 8")().then(v=>asyncResult=v)`)
	if got := evalString(t, s, `asyncResult`); got != "8" {
		t.Fatal(got)
	}
}

func TestNativeDynamicCompilationDisablesSourceMaps(t *testing.T) {
	const mapJSON = `{"version":3,"sources":["SECRET.js"],"names":[],"mappings":"AAAA"}`
	for _, expr := range []string{`eval(payload)`, `(0,eval)(payload)`, `Function(payload)()`, `(async function(){}).constructor(payload)()`, `(function*(){}).constructor(payload)().next()`} {
		t.Run(expr, func(t *testing.T) {
			calls := 0
			loader := func(string) ([]byte, error) { calls++; return []byte(mapJSON), nil }
			run := func(vm *goja.Runtime) {
				_ = vm.Set("payload", "1;\n//# sourceMappingURL=file:///not-readable-by-page.map\n")
				ast, err := goja.Parse("test.js", expr, parser.WithDisableSourceMaps)
				if err != nil {
					t.Fatal(err)
				}
				program, err := goja.CompileAST(ast, false)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = vm.RunProgram(program); err != nil {
					t.Fatal(err)
				}
			}
			control := goja.New()
			control.SetParserOptions(parser.WithSourceMapLoader(loader))
			run(control)
			if calls == 0 {
				t.Fatal("positive control did not try to load source map")
			}
			calls = 0
			s := dynamicSandbox(t, true)
			// Add a trap loader while retaining the production disabled-source-map option.
			opts := append(sandboxParserOptions(), parser.WithSourceMapLoader(loader))
			s.vm.SetParserOptions(opts...)
			run(s.vm)
			if calls != 0 {
				t.Fatal("dynamic compiler accessed source map")
			}
		})
	}
}

func TestDynamicCodeRemainsInsideWatchdog(t *testing.T) {
	for _, src := range []string{`eval("for(;;){}")`, `Function("for(;;){}")()`} {
		s := dynamicSandbox(t, true)
		s.cfg.Slice = 10 * time.Millisecond
		s.cfg.Strikes = 1
		if err := s.Eval("spin.js", src); err == nil || !s.Disabled() {
			t.Fatalf("runaway dynamic code escaped watchdog: %v", err)
		}
	}
}

func TestDynamicCodeDoesNotExposeHostPrivileges(t *testing.T) {
	s := dynamicSandbox(t, true)
	for _, src := range []string{`eval("[typeof require,typeof process,typeof WebAssembly].join()")`, `Function("return [typeof require,typeof process,typeof WebAssembly].join()")()`} {
		if got := evalString(t, s, src); got != "undefined,undefined,undefined" {
			t.Fatal(got)
		}
	}
	if err := s.Eval("timer.js", `setTimeout("1",0)`); err == nil {
		t.Fatal("string timer opened a new compilation path")
	}
}
