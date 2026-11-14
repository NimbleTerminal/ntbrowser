// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"fmt"
	"golang.org/x/net/html"
	"strings"
)

type FrameImage struct {
	Source        string
	Width, Height int
}

func (t *Tree) SerializeFrames(frames map[NodeID]FrameImage) string {
	t.frames = frames
	defer func() { t.frames = nil }()
	return t.Serialize()
}

// SerializePresentation snapshots HTML and the matching renderer-only IDs of
// shown popovers together. Neither IDs nor shown state become DOM attributes.
func (t *Tree) SerializePresentation(frames map[NodeID]FrameImage) (string, []string) {
	t.frames = frames
	defer func() { t.frames = nil }()
	return t.serializePresentation()
}

func (t *Tree) writeFrame(b *strings.Builder, n *Node, f FrameImage, openPopovers *[]string) {
	own, _ := n.Attr("id")
	id := frameID(n, t.ids[own])
	if id == "" {
		id = syntheticID(n.id)
	}
	if n.popoverOpen {
		*openPopovers = append(*openPopovers, id)
	}
	if own != "" {
		t.ids[own] = true
	}
	b.WriteString("<img")
	for _, a := range n.attrs {
		switch a.Name {
		case "class", "style", "title", "popover":
		default:
			continue
		}
		fmt.Fprintf(b, " %s=\"%s\"", a.Name, html.EscapeString(a.Value))
	}
	fmt.Fprintf(b, " id=\"%s\" src=\"%s\" width=\"%d\" height=\"%d\">", html.EscapeString(id), html.EscapeString(f.Source), f.Width, f.Height)
}
