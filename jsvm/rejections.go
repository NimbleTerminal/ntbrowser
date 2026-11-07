// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"fmt"
	"strings"

	"github.com/dop251/goja"
)

func (s *Sandbox) logPromiseRejection(reason goja.Value) {
	// Formatting a page value can itself reject a promise. Do not recursively
	// enter the logger; all formatting still runs under the active watchdog.
	if s.rejectionReporting {
		return
	}
	s.rejectionReporting = true
	defer func() { s.rejectionReporting = false }()

	// Goja preserves an Error's native creation stack when converting a thrown
	// value to an Exception. For other values it captures the rejection site.
	// Never read reason.stack: it may be forged or an arbitrary accessor.
	exception := s.vm.Try(func() { panic(reason) })
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
	if exception != nil {
		count := 0
		for _, frame := range exception.Stack() {
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
