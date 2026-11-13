// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import "strings"

// SetToggleHook observes committed disclosure state, without entering page JS.
// Shared-realm callers may invoke it; delivery belongs to the owning checkpoint.
func (t *Tree) SetToggleHook(h func(*Node, bool, bool)) { t.toggleHook = h }
func (t *Tree) toggled(n *Node, old, next bool) {
	if old != next && t.toggleHook != nil {
		t.toggleHook(n, old, next)
	}
}
func (n *Node) PopoverOpen() bool { return n.popoverOpen }
func (n *Node) PopoverMode() string {
	v, ok := n.Attr("popover")
	if !ok {
		return ""
	}
	switch strings.ToLower(v) {
	case "manual", "hint":
		return strings.ToLower(v)
	default:
		return "auto"
	}
}

// SetPopoverOpen changes presentation only; the caller handles beforetoggle.
// State is private, bounded per node, and never reflected into HTML attributes.
func (t *Tree) SetPopoverOpen(n *Node, open bool) bool {
	if n == nil || n.PopoverMode() == "" || open && !t.Attached(n) {
		return false
	}
	old := n.popoverOpen
	n.popoverOpen = open
	if old != open {
		t.dirty = true
		t.toggled(n, old, open)
	}
	return true
}

// OpenPopoverIDs uses the same collision-safe ids as Serialize. Author ids
// cannot impersonate another node's open state, including reserved nt-h ids.
func (t *Tree) OpenPopoverIDs() []string {
	anchor := t.Anchors()
	var ids []string
	Walk(t.root, func(n *Node) {
		if n.popoverOpen {
			ids = append(ids, anchor(n))
		}
	})
	return ids
}

func (t *Tree) disclosureAttr(n *Node, name string, oldPresent bool) {
	if name == "popover" && n.popoverOpen {
		t.SetPopoverOpen(n, false)
	}
	if n.tag != "details" {
		return
	}
	if name == "open" {
		t.toggled(n, oldPresent, true)
	}
	if name == "open" || name == "name" {
		t.enforceDetails(n)
	}
}
func (t *Tree) enforceDetails(n *Node) {
	name, _ := n.Attr("name")
	_, open := n.Attr("open")
	if n.tag != "details" || name == "" || !open {
		return
	}
	Walk(t.RootOf(n), func(other *Node) {
		if other != n && other.tag == "details" {
			if group, _ := other.Attr("name"); group == name {
				t.RemoveAttr(other, "open")
			}
		}
	})
}

// Initial/inserted markup uses first-in-tree order. One pass per committed
// subtree insertion; ordinary attribute changes let the changed member win.
func (t *Tree) normalizeDetails(root *Node) {
	seen := map[string]bool{}
	Walk(root, func(n *Node) {
		if n.tag != "details" {
			return
		}
		name, _ := n.Attr("name")
		_, open := n.Attr("open")
		if name == "" || !open {
			return
		}
		if seen[name] {
			t.RemoveAttr(n, "open")
		} else {
			seen[name] = true
		}
	})
}

// PopoverPeers returns currently shown peers to close before showing n.
// Manual popovers and ancestors are independent of this peer policy.
func (t *Tree) PopoverPeers(n *Node) []*Node {
	if n.PopoverMode() == "manual" {
		return nil
	}
	var peers []*Node
	Walk(t.root, func(other *Node) {
		if !other.popoverOpen || other == n || other.PopoverMode() == "manual" {
			return
		}
		if n.PopoverMode() == "hint" && other.PopoverMode() != "hint" {
			return
		}
		for p := n.parent; p != nil; p = p.parent {
			if p == other {
				return
			}
		}
		peers = append(peers, other)
	})
	return peers
}

// normalizeInsertedDetails gives existing open members priority over a newly
// inserted subtree, even when that subtree was inserted earlier in tree order.
func (t *Tree) normalizeInsertedDetails(inserted *Node) {
	seen := map[string]bool{}
	var visit func(*Node)
	visit = func(n *Node) {
		if n == inserted {
			return
		}
		if n.tag == "details" {
			name, _ := n.Attr("name")
			_, open := n.Attr("open")
			if name != "" && open {
				seen[name] = true
			}
		}
		for _, c := range n.children {
			visit(c)
		}
	}
	visit(t.RootOf(inserted))
	Walk(inserted, func(n *Node) {
		if n.tag != "details" {
			return
		}
		name, _ := n.Attr("name")
		_, open := n.Attr("open")
		if name == "" || !open {
			return
		}
		if seen[name] {
			t.RemoveAttr(n, "open")
		} else {
			seen[name] = true
		}
	})
}
