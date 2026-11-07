// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
)

func (s *Sandbox) setInnerHTML(target, value goja.Value) {
	n := s.node(target)
	source := ""
	if !goja.IsNull(value) {
		str := value.ToString()
		if v, ok := str.(goja.String); ok && v.Length() > dom.MaxFragmentBytes {
			s.throw(dom.ErrLimit)
		}
		source = str.String()
	}
	previous := n.Children()
	s.underPressure(func() error { return s.tree.SetInnerHTML(n, source) })
	for _, old := range previous {
		s.onDetach(old)
	}
	for _, root := range s.tree.DetachedRoots() {
		if !s.anyLiveWrapper(root) {
			dom.Walk(root, func(n *dom.Node) { s.forgetCounts(n.ID()) })
			s.tree.Release(root)
		}
	}
	// Fragment script nodes are parser-marked, so they never execute, including
	// after a move or src edit. The existing target may itself be a script.
	s.prepareScript(n)
	s.prepareScript(n.Parent())
}
