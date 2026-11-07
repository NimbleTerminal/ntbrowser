// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"fmt"
	"strings"

	"github.com/dop251/goja"
)

const maxPendingRejections = 256

type pendingRejection struct {
	promise *goja.Promise
	frames  []goja.StackFrame
}

func (s *Sandbox) trackPromiseRejection(p *goja.Promise, op goja.PromiseRejectionOperation) {
	if op == goja.PromiseRejectionHandle {
		for i, r := range s.pendingRejections {
			if r.promise == p {
				copy(s.pendingRejections[i:], s.pendingRejections[i+1:])
				s.pendingRejections[len(s.pendingRejections)-1] = pendingRejection{}
				s.pendingRejections = s.pendingRejections[:len(s.pendingRejections)-1]
				break
			}
		}
		return
	}
	if s.rejectionReporting {
		return
	}
	if len(s.pendingRejections) >= maxPendingRejections {
		s.rejectionsDropped = true
		return
	}
	// Preserve native Error creation frames, or the rejection site for other
	// values, before the stack unwinds. Never access a page's stack property.
	ex := s.vm.Try(func() { panic(p.Result()) })
	var frames []goja.StackFrame
	if ex != nil {
		stack := ex.Stack()
		frames = append(frames, stack[:min(len(stack), 16)]...)
	}
	s.pendingRejections = append(s.pendingRejections, pendingRejection{p, frames})
}

func (s *Sandbox) flushPromiseRejections() error {
	if len(s.pendingRejections) == 0 && !s.rejectionsDropped {
		return nil
	}
	s.rejectionReporting = true
	defer func() { s.rejectionReporting = false }()
	// Use a runtime call so hostile formatting still returns interrupts through
	// Run's normal watchdog/strike handling. No function is exposed to the page.
	report, _ := goja.AssertFunction(s.vm.ToValue(func(goja.FunctionCall) goja.Value {
		for len(s.pendingRejections) > 0 {
			r := s.pendingRejections[0]
			s.pendingRejections[0] = pendingRejection{}
			s.pendingRejections = s.pendingRejections[1:]
			s.logPromiseRejection(r.promise.Result(), r.frames)
		}
		if s.rejectionsDropped {
			s.host.Log("warn", "Promise rejection diagnostic limit reached; some rejections were not tracked")
		}
		return goja.Undefined()
	}))
	_, err := report(goja.Undefined())
	return err
}

func (s *Sandbox) logPromiseRejection(reason goja.Value, frames []goja.StackFrame) {
	message := "<unprintable rejection>"
	if ex := s.vm.Try(func() { message = safeString(reason) }); ex != nil {
		message = "<unprintable rejection>"
	}
	if len(message) > 512 {
		message = message[:512] + "…"
	}
	var out strings.Builder
	out.WriteString("Unhandled promise rejection: ")
	out.WriteString(message)
	if len(frames) > 0 {
		count := 0
		for _, frame := range frames {
			pos := frame.Position()
			if pos.Line <= 0 {
				continue
			}
			fmt.Fprintf(&out, " at %s (%s:%d:%d)", frame.FuncName(), pos.Filename, pos.Line, pos.Column)
			count++
			if count == 12 || out.Len() >= 4096 {
				break
			}
		}
	}
	s.host.Log("error", safeString(s.vm.ToValue(out.String())))
}
