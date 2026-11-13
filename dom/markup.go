// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Fragment limits bound the non-preemptible HTML parser, independently of the
// live DOM budget. Parsed scripts remain permanently inert.
const MaxFragmentBytes = 64 << 10
const MaxFragmentTokens = 4096

func (t *Tree) SetInnerHTML(n *Node, source string) error {
	if n == nil || t.nodes[n.id] != n || n.kind != Element || n == t.root {
		return fmt.Errorf("ntbrowser/dom: innerHTML requires an element")
	}
	if len(source) > MaxFragmentBytes {
		return fmt.Errorf("%w: HTML fragment exceeds %d bytes", ErrLimit, MaxFragmentBytes)
	}
	// Tokenize before tree construction to bound work even for malformed markup
	// whose parser repair would otherwise create many implicit nodes.
	z := html.NewTokenizer(strings.NewReader(source))
	for count := 0; ; count++ {
		typ := z.Next()
		if typ == html.ErrorToken {
			if z.Err() != io.EOF {
				return z.Err()
			}
			break
		}
		if count >= MaxFragmentTokens {
			return fmt.Errorf("%w: HTML fragment token cap", ErrLimit)
		}
	}
	context := &html.Node{Type: html.ElementNode, Data: n.tag, DataAtom: atom.Lookup([]byte(n.tag))}
	// Preserve the existing DOM's SVG/foreignObject interpretation.
	for p := n; p != nil; p = p.parent {
		if p.tag == "foreignobject" {
			break
		}
		if p.tag == "svg" {
			context.Namespace = "svg"
			break
		}
	}
	parsed, err := html.ParseFragment(strings.NewReader(source), context)
	if err != nil {
		return err
	}
	root := &html.Node{Type: html.DocumentNode}
	for _, p := range parsed {
		root.AppendChild(p)
	}
	staged := &Tree{limits: t.limits, nodes: map[NodeID]*Node{}, detached: map[NodeID]*Node{}, styles: map[string]*injectedStyle{}}
	staged.root = staged.newNode(Element, n.tag)
	if err := staged.ingest(staged.root, root, 0); err != nil {
		return err
	}
	if t.live+staged.live > t.limits.Nodes || t.contentBytes()+staged.bytes > t.limits.TotalBytes {
		return fmt.Errorf("%w: fragment exceeds live DOM budget", ErrLimit)
	}
	for _, c := range staged.root.children {
		if depthOf(n)+height(c) > t.limits.Depth {
			return fmt.Errorf("%w: fragment depth", ErrLimit)
		}
	}
	// All checks precede mutation; old wrappers retain their detached subtrees.
	removed := n.Children()
	for _, c := range n.children {
		Walk(c, func(v *Node) {
			if v.popoverOpen {
				t.SetPopoverOpen(v, false)
			}
		})
		c.parent = nil
		t.detached[c.id] = c
	}
	n.children = staged.root.children
	for _, c := range n.children {
		c.parent = n
		Walk(c, func(v *Node) { t.nextID++; v.id = t.nextID; t.nodes[v.id] = v })
	}
	t.live += staged.live
	t.bytes += staged.bytes
	t.dirty = true
	if len(removed)+len(n.children) > 0 {
		t.emitMutation(Mutation{Type: "childList", Target: n, Removed: removed, Added: n.Children()})
	}
	t.normalizeInsertedDetails(n)
	return nil
}

// InnerHTML serializes actual DOM children, without renderer-only IDs, form
// state, or injected styles. Unlike renderer serialization, inert scripts are
// included so markup can round-trip without conferring execution authority.
func (t *Tree) InnerHTML(n *Node) string {
	var b strings.Builder
	var convert func(*Node) *html.Node
	convert = func(v *Node) *html.Node {
		if v.kind == Text {
			return &html.Node{Type: html.TextNode, Data: v.text}
		}
		h := &html.Node{Type: html.ElementNode, Data: v.tag, DataAtom: atom.Lookup([]byte(v.tag))}
		for _, a := range v.attrs {
			h.Attr = append(h.Attr, html.Attribute{Key: a.Name, Val: a.Value})
		}
		for _, c := range v.children {
			h.AppendChild(convert(c))
		}
		return h
	}
	context := convert(n)
	for c := context.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && (n.tag == "script" || n.tag == "style" || n.tag == "xmp" || n.tag == "iframe" || n.tag == "noembed" || n.tag == "noframes" || n.tag == "plaintext" || n.tag == "noscript") {
			b.WriteString(c.Data)
		} else {
			_ = html.Render(&b, c)
		}
	}
	return b.String()
}
