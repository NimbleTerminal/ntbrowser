// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"strings"
	"testing"
)

func TestPresentationPopoverIDs(t *testing.T) {
	tree, err := Parse(strings.NewReader(`<div id=dup popover>closed</div><div id=dup popover>open</div><div popover>unnamed</div><div id=nt-h-999 popover>reserved</div><script><div popover>inert</div></script>`), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var nodes []*Node
	Walk(tree.Root(), func(n *Node) {
		if n.PopoverMode() != "" {
			nodes = append(nodes, n)
		}
	})
	for _, n := range nodes[1:] {
		tree.SetPopoverOpen(n, true)
	}
	html, ids := tree.SerializePresentation(nil)
	if len(ids) != 3 {
		t.Fatalf("ids = %v", ids)
	}
	for i, id := range ids {
		if id == "dup" || !strings.Contains(html, `id="`+id+`"`) {
			t.Fatalf("incorrect render id %q in %s", id, html)
		}
		if id != AnchorID(tree, nodes[i+1]) {
			t.Fatalf("anchor mismatch: %q", id)
		}
	}
	markup := tree.InnerHTML(tree.Root())
	if strings.Contains(markup, `id="`+ids[0]+`"`) || strings.Contains(markup, `id="`+ids[1]+`"`) {
		t.Fatalf("renderer IDs leaked: %s", markup)
	}
	tree.SetPopoverOpen(nodes[1], false)
	_, next := tree.SerializePresentation(nil)
	if len(next) != 2 || len(ids) != 3 {
		t.Fatalf("state not snapshotted: %v / %v", ids, next)
	}
}

func TestFramePresentationPopover(t *testing.T) {
	tree, err := Parse(strings.NewReader(`<iframe popover id=f></iframe>`), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	n := tree.ByID("f")
	frames := map[NodeID]FrameImage{n.ID(): {Source: "frame", Width: 100, Height: 100}}
	html, ids := tree.SerializePresentation(frames)
	if !strings.Contains(html, `popover=""`) || len(ids) != 0 {
		t.Fatalf("closed frame: %s %v", html, ids)
	}
	tree.SetPopoverOpen(n, true)
	_, ids = tree.SerializePresentation(frames)
	if len(ids) != 1 || ids[0] != "f" {
		t.Fatalf("shown frame: %v", ids)
	}
}
