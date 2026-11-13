// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"strings"
	"testing"
)

func TestDisclosureGroupsAndPrivateState(t *testing.T) {
	tree, err := Parse(strings.NewReader(`<details id=a name=g open></details><section><details id=b name=g open></details></section><details id=c open></details><div id=p popover></div><div id=p popover></div>`), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	a, b := tree.ByID("a"), tree.ByID("b")
	if _, ok := b.Attr("open"); ok {
		t.Fatal("second initial member open")
	}
	if err := tree.SetAttr(b, "open", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Attr("open"); ok {
		t.Fatal("old member still open")
	}
	if err := tree.SetAttr(tree.ByID("c"), "name", "g"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Attr("open"); ok {
		t.Fatal("name change did not close peer")
	}
	var popovers []*Node
	Walk(tree.Root(), func(n *Node) {
		if n.PopoverMode() != "" {
			popovers = append(popovers, n)
		}
	})
	if !tree.SetPopoverOpen(popovers[1], true) {
		t.Fatal("show failed")
	}
	ids := tree.OpenPopoverIDs()
	if len(ids) != 1 || ids[0] == "p" {
		t.Fatalf("ambiguous ids: %v", ids)
	}
	markup := tree.InnerHTML(popovers[1].Parent())
	if strings.Contains(markup, "nt-h-") {
		t.Fatal("render state leaked")
	}
	if !strings.Contains(tree.Serialize(), `id="`+ids[0]+`"`) {
		t.Fatal("id mismatch")
	}
	tree.Detach(popovers[1])
	if popovers[1].PopoverOpen() {
		t.Fatal("detached popover remains shown")
	}
	if err := tree.SetInnerHTML(tree.ByID("a"), `<details name=x open id=x1></details><details name=x open id=x2></details>`); err != nil {
		t.Fatal(err)
	}
	if _, ok := tree.ByID("x2").Attr("open"); ok {
		t.Fatal("fragment groups not normalized")
	}
}

func TestInsertedDetailsPreserveExistingOpenMember(t *testing.T) {
	tree, err := Parse(strings.NewReader(`<section id=parent><details id=existing name=g open></details></section>`), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	added, err := tree.NewElement("details")
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.SetAttr(added, "name", "g"); err != nil {
		t.Fatal(err)
	}
	if err := tree.SetAttr(added, "open", ""); err != nil {
		t.Fatal(err)
	}
	if err := tree.InsertBefore(tree.ByID("parent"), added, tree.ByID("existing")); err != nil {
		t.Fatal(err)
	}
	if _, open := added.Attr("open"); open {
		t.Fatal("inserted member displaced existing member")
	}
	if _, open := tree.ByID("existing").Attr("open"); !open {
		t.Fatal("existing member closed")
	}
}
