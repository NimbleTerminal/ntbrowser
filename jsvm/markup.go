// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"strings"

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

// documentWrite inserts markup where a browser would: after the running
// parser-inserted script, else at the end of the body. The markup is the
// same bounded fragment parse as innerHTML, so its scripts stay inert (a
// written <script src> is reported once); the document is never cleared.
func (s *Sandbox) documentWrite(args []goja.Value, newline bool) {
	var b strings.Builder
	for _, a := range args {
		b.WriteString(a.String())
		if b.Len() > dom.MaxFragmentBytes {
			s.throw(dom.ErrLimit)
		}
	}
	if newline {
		b.WriteByte('\n')
	}
	source := b.String()
	if strings.TrimSpace(source) == "" {
		return
	}
	// The insertion point follows the running script and advances past what
	// earlier writes from the same script inserted.
	parent, ref := s.tree.Body(), (*dom.Node)(nil)
	if cur := s.tree.Node(s.currentScript); cur != nil && cur.Parent() != nil {
		parent = cur.Parent()
		anchor := cur
		if s.writeScript == s.currentScript {
			if last := s.tree.Node(s.writeCursor); last != nil && last.Parent() == parent {
				anchor = last
			}
		}
		siblings := parent.Children()
		for i, c := range siblings {
			if c == anchor && i+1 < len(siblings) {
				ref = siblings[i+1]
				break
			}
		}
	}
	if parent == nil {
		return
	}
	var holder *dom.Node
	s.underPressure(func() (err error) { holder, err = s.tree.NewElement("div"); return err })
	s.underPressure(func() error { return s.tree.SetInnerHTML(holder, source) })
	if strings.Contains(strings.ToLower(source), "<script") && !s.writeScriptWarned {
		s.writeScriptWarned = true
		s.host.Log("warn", "document.write: written scripts are not executed")
	}
	for _, child := range holder.Children() {
		child := child
		s.underPressure(func() error { return s.tree.InsertBefore(parent, child, ref) })
		s.writeScript, s.writeCursor = s.currentScript, child.ID()
	}
	s.tree.Release(holder)
	s.prepareScript(parent)
}
