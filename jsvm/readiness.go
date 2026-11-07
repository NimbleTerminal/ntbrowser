// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import "github.com/dop251/goja"

// SetDocumentReady advances the host-owned loading lifecycle. Page scripts
// cannot mutate it. Calls are confined to the sandbox's owning goroutine.
func (s *Sandbox) SetDocumentReady(state string) error {
	if s.disabled {
		return ErrDisabled
	}
	if !(s.documentReady == "loading" && state == "interactive" || s.documentReady == "interactive" && state == "complete") {
		return nil
	}
	s.documentReady = state
	_, err := s.Dispatch(Event{Type: "readystatechange", NoBubble: true, NonCancelable: true})
	return err
}

// DispatchDocumentLoad fires the window load event with its legacy document
// target, but no document propagation path. Element loads remain independent.
func (s *Sandbox) DispatchDocumentLoad() error {
	return s.Run(func(*goja.Runtime) error {
		e := s.newEvent("load", false, false, false, s.eventProto)
		st := s.brand(e, "Event")
		_ = st.Set("trusted", true)
		_ = st.Set("documentLoad", true)
		_, err := s.dispatchEvent(s.vm.GlobalObject(), e)
		return err
	})
}
