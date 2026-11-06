// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import "github.com/dop251/goja"

// configureDynamicCode runs before any page code. Enabled mode keeps the real
// eval intrinsic: wrapping it would turn direct eval into global-scope eval.
// Goja's native eval and Function family use Runtime.parserOptions, already
// set by harden, and stay within the current watchdog entry. Goja offers no
// dynamic source-length hook or parse/compile preemption; see the gate docs.
func (s *Sandbox) configureDynamicCode() {
	if s.cfg.AllowDynamicCode {
		return
	}
	vm := s.vm
	// Capture EvalError's prototype before scripts can replace the global.
	errorProto := vm.Get("EvalError").ToObject(vm).Get("prototype").ToObject(vm)
	deny := func() {
		e := vm.NewTypeError("dynamic-code is off in this profile")
		_ = e.SetPrototype(errorProto)
		panic(e)
	}
	defineFrozen(vm.GlobalObject(), "eval", vm.ToValue(func(goja.FunctionCall) goja.Value { deny(); return goja.Undefined() }))
	// This fixed bootstrap contains no page text. It materializes lazy engine
	// prototypes using the same explicit hardened parse/CompileAST route as
	// Sandbox.Eval. Never discover prototypes through page-controlled code.
	const source = `[Object.getPrototypeOf(function(){}),Object.getPrototypeOf(async function(){}),Object.getPrototypeOf(function*(){})]`
	ast, err := goja.Parse("ntbrowser:dynamic-code-prototypes", source, sandboxParserOptions()...)
	if err != nil {
		panic(err)
	}
	program, err := goja.CompileAST(ast, false)
	if err != nil {
		panic(err)
	}
	value, err := vm.RunProgram(program)
	if err != nil {
		panic(err)
	}
	prototypes := value.ToObject(vm)
	for _, key := range []string{"0", "1", "2"} {
		proto := prototypes.Get(key).ToObject(vm)
		blocked := vm.ToValue(func(goja.ConstructorCall) *goja.Object { deny(); return nil }).(*goja.Object)
		_ = blocked.Set("prototype", proto)
		defineFrozen(proto, "constructor", blocked)
		if key == "0" {
			defineFrozen(vm.GlobalObject(), "Function", blocked)
		}
	}
}
