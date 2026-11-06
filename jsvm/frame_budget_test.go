// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"github.com/dop251/goja"
	"testing"
	"time"
)

func TestFrameBudgetInterruptsAndRestoresSlice(t *testing.T) {
	s := dynamicSandbox(t, false)
	s.cfg.Slice = time.Second
	s.cfg.Strikes = 1
	imageEval(t, s, `function runaway(){for(;;){}}`)
	fn, _ := goja.AssertFunction(s.vm.Get("runaway"))
	if err := s.CallWithinBudget(time.Millisecond, fn, goja.Undefined()); err == nil || !s.Disabled() {
		t.Fatal("shared budget failed to interrupt", err)
	}
	if s.cfg.Slice != time.Second {
		t.Fatal("shortened budget leaked into other callbacks")
	}
}
