// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"fmt"
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
	"strings"
)

const MaxDynamicScripts = 256
const MaxPendingScripts = 32

type ScriptRequest struct {
	ID             dom.NodeID
	URL, Integrity string
	CrossOrigin    string
	Async          bool
}
type scriptState struct {
	started, ordered bool
	pin              *goja.Object
}

func (s *Sandbox) scriptNode(v goja.Value) *dom.Node {
	n := s.node(v)
	if n.Tag() != "script" {
		panic(s.vm.NewTypeError("not an HTMLScriptElement"))
	}
	return n
}
func (s *Sandbox) scriptState(n *dom.Node) *scriptState {
	st := s.scripts[n.ID()]
	if st == nil {
		st = &scriptState{}
		s.scripts[n.ID()] = st
	}
	return st
}
func (s *Sandbox) installScripts() {
	s.scripts = map[dom.NodeID]*scriptState{}
	p := s.vm.NewObject()
	s.scriptProto = p
	_ = p.SetPrototype(s.elemProto)
	ctor := s.vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(s.vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(s.vm.GlobalObject(), "HTMLScriptElement", ctor)
	for _, name := range []string{"src", "type", "integrity", "crossOrigin", "referrerPolicy", "nonce"} {
		attr := strings.ToLower(name)
		s.accessor(p, name, func(t goja.Value) goja.Value {
			value, present := s.scriptNode(t).Attr(attr)
			if name == "src" && present {
				value = s.imageURL(value)
			}
			return s.vm.ToValue(value)
		}, func(t, v goja.Value) {
			n := s.scriptNode(t)
			s.must(s.tree.SetAttr(n, attr, v.String()))
			if name == "src" {
				s.prepareScript(n)
			}
		})
	}
	for _, name := range []string{"defer", "noModule"} {
		attr := strings.ToLower(name)
		s.accessor(p, name, func(t goja.Value) goja.Value { _, ok := s.scriptNode(t).Attr(attr); return s.vm.ToValue(ok) }, func(t, v goja.Value) {
			n := s.scriptNode(t)
			if v.ToBoolean() {
				s.must(s.tree.SetAttr(n, attr, ""))
			} else {
				s.tree.RemoveAttr(n, attr)
			}
		})
	}
	s.accessor(p, "text", func(t goja.Value) goja.Value { return s.vm.ToValue(dom.TextContent(s.scriptNode(t))) }, s.setText)
	s.accessor(p, "async", func(t goja.Value) goja.Value {
		n := s.scriptNode(t)
		_, attr := n.Attr("async")
		return s.vm.ToValue(!s.scriptState(n).ordered || attr)
	}, func(t, v goja.Value) {
		n := s.scriptNode(t)
		s.scriptState(n).ordered = true
		if v.ToBoolean() {
			s.must(s.tree.SetAttr(n, "async", ""))
		} else {
			s.tree.RemoveAttr(n, "async")
		}
	})

}

// prepareScript is called at insertion/connected src or text mutation, never by
// the renderer. Started state prevents moves, removals and src edits replaying it.
func (s *Sandbox) prepareScript(n *dom.Node) {
	if n == nil || n.Tag() != "script" || n.ParserScript() || !s.tree.Attached(n) || !s.cfg.DynamicScripts || s.disabled {
		return
	}
	st := s.scriptState(n)
	if st.started {
		return
	}
	typ, _ := n.Attr("type")
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "", "text/javascript", "application/javascript", "text/ecmascript", "application/ecmascript":
	default:
		return // modules and data blocks remain inert
	}
	src, external := n.Attr("src")
	text := dom.TextContent(n)
	if !external && text == "" {
		return
	}
	if s.scriptCount >= MaxDynamicScripts {
		panic(s.vm.NewTypeError("dynamic script limit exceeded"))
	}
	if external {
		pending := 0
		for _, state := range s.scripts {
			if state.pin != nil {
				pending++
			}
		}
		if pending >= MaxPendingScripts {
			panic(s.vm.NewTypeError("too many pending dynamic scripts"))
		}
	}
	st.started = true
	s.scriptCount++
	if !external {
		if !s.cfg.AllowDynamicCode {
			s.host.Log("warn", "dynamic inline script blocked: dynamic-code is off")
			return
		}
		// Reuse the caller's watchdog; do not rearm or clear its interrupt.
		previous := s.currentScript
		s.currentScript = n.ID()
		defer func() { s.currentScript = previous }()
		err := s.evalInserted(s.cfg.Location+"#dynamic-inline", text)
		if err != nil {
			if _, ok := err.(*goja.InterruptedError); ok {
				panic(err)
			}
			s.host.Log("error", err.Error())
		}
		return
	}
	st.pin = s.wrap(n).(*goja.Object)
	integrity, _ := n.Attr("integrity")
	_, asyncAttr := n.Attr("async")
	if s.cfg.LoadScript == nil {
		s.CompleteScript(n.ID(), "", "", fmt.Errorf("dynamic script loading disabled"))
		return
	}
	if strings.TrimSpace(src) != "" {
		src = s.imageURL(src)
	} else {
		src = ""
	}
	corsMode, present := n.Attr("crossorigin")
	if !present {
		corsMode = "none"
	} else if !strings.EqualFold(corsMode, "use-credentials") {
		corsMode = "anonymous"
	}
	s.cfg.LoadScript(ScriptRequest{CrossOrigin: corsMode, ID: n.ID(), URL: src, Integrity: integrity, Async: !st.ordered || asyncAttr})
}

func (s *Sandbox) evalInserted(name, src string) error {
	if len(src) > maxScriptBytes {
		return fmt.Errorf("ntbrowser/jsvm: script over %d bytes", maxScriptBytes)
	}
	ast, err := goja.Parse(name, src, sandboxParserOptions()...)
	if err != nil {
		return err
	}
	program, err := goja.CompileAST(ast, false)
	if err != nil {
		return err
	}
	if s.running {
		_, err = s.vm.RunProgram(program)
		return err
	}
	return s.Run(func(vm *goja.Runtime) error { _, err := vm.RunProgram(program); return err })
}

// CompleteScript runs only on the owning VM goroutine. The loop discards stale
// page completions before calling it. Pending pins retain detached handlers.
func (s *Sandbox) CompleteScript(id dom.NodeID, name, source string, loadErr error) {
	if s.disabled {
		return
	}
	st := s.scripts[id]
	if st == nil || st.pin == nil {
		return
	}
	defer func() { st.pin = nil }()
	event := "load"
	if loadErr != nil {
		event = "error"
	} else {
		previous := s.currentScript
		s.currentScript = id
		err := s.Eval(name, source)
		s.currentScript = previous
		if err != nil {
			s.host.Log("error", err.Error())
		}
	}
	if !s.disabled {
		_, err := s.Dispatch(Event{Type: event, Target: id, NoBubble: true, AllowDetached: true, NonCancelable: true})
		if err != nil {
			s.host.Log("error", err.Error())
		}
	}
}
