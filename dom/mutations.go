// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

// Mutation contains committed DOM data only. The observer hook must not invoke
// page code synchronously or reject a mutation after it has committed.
type Mutation struct {
	Type, AttributeName, OldValue        string
	OldPresent                           bool
	Target, PreviousSibling, NextSibling *Node
	Added, Removed                       []*Node
}

func (t *Tree) SetMutationHook(hook func(Mutation)) { t.mutationHook = hook }
func (t *Tree) emitMutation(m Mutation) {
	if t.mutationHook != nil {
		t.mutationHook(m)
	}
}
func siblings(n *Node) (previous, next *Node) {
	if n.parent == nil {
		return
	}
	for i, c := range n.parent.children {
		if c == n {
			if i > 0 {
				previous = n.parent.children[i-1]
			}
			if i+1 < len(n.parent.children) {
				next = n.parent.children[i+1]
			}
			return
		}
	}
	return
}
