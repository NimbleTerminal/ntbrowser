// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"sort"

	"nimbleterminal.dev/ntbrowser/devtools"
	"nimbleterminal.dev/ntbrowser/dom"
)

// Inspect copies plain data on the owner loop. No Goja calls or pointers escape.
func (l *Loop) Inspect(ctx context.Context, r devtools.Request) (devtools.Response, error) {
	if !l.cfg.Profile.DevTools {
		return devtools.Response{Error: "disabled"}, nil
	}
	done := make(chan devtools.Response, 1)
	if !l.tryPost(func() {
		if ctx.Err() != nil {
			return
		}
		done <- l.inspectOwned(r)
	}) {
		return devtools.Response{Error: "busy"}, nil
	}
	select {
	case out := <-done:
		return out, nil
	case <-ctx.Done():
		return devtools.Response{}, ctx.Err()
	case <-l.ctx.Done():
		return devtools.Response{}, context.Canceled
	}
}
func (l *Loop) inspectOwned(r devtools.Request) devtools.Response {
	if !l.cfg.Profile.DevTools {
		return devtools.Response{Error: "disabled"}
	}
	realms := map[uint64]*Loop{}
	var collect func(*Loop)
	collect = func(owner *Loop) {
		if owner.page == nil || owner.page.debugNavigation != owner.navGen.Load() || owner.ctx.Err() != nil {
			return
		}
		id := owner.page.frameID
		if owner == l {
			id = 0
		}
		realms[id] = owner
		for _, f := range owner.page.frames {
			if f.child != nil && frameDOMCurrent(owner.cfg.frameHub, f.child) {
				collect(f.child)
			}
		}
	}
	collect(l)
	switch r.Method {
	case "snapshot":
		snap := &devtools.Snapshot{Policy: devtools.Clip(l.cfg.Profile.Table(nil), 8192)}
		for id, owner := range realms {
			p := owner.page
			parent := owner.cfg.frameParent
			if owner == l || parent == l.page.frameID {
				parent = 0
			}
			snap.Frames = append(snap.Frames, devtools.Frame{ID: id, Revision: p.debugRevision, Parent: parent, URL: devtools.URL(p.Base), Nodes: p.Tree.Live(), DOMBytes: p.Tree.Bytes(), Scripts: len(p.Tree.Scripts), Timers: len(owner.timers), AnimationCallbacks: len(owner.frames), PendingScripts: p.dynamicPending, Loaded: p.loadComplete, Disabled: p.killed})
		}
		sort.Slice(snap.Frames, func(i, j int) bool { return snap.Frames[i].ID < snap.Frames[j].ID })
		return devtools.Response{Snapshot: snap}
	case "dom":
		target := realms[r.Frame]
		if target == nil || target.page.debugRevision != r.Revision {
			return devtools.Response{Error: "stale_document"}
		}
		tree := target.page.Tree
		n := tree.Root()
		if r.Node != 0 {
			n = tree.Node(dom.NodeID(r.Node))
		}
		if n == nil || !tree.Attached(n) {
			return devtools.Response{Error: "missing_node"}
		}
		limit := r.Limit
		if limit <= 0 || limit > devtools.MaxItems {
			limit = devtools.MaxItems
		}
		children := n.Children()
		out := &devtools.DOM{Frame: r.Frame, Revision: r.Revision, Node: inspectNode(n), Next: -1}
		if r.Offset < 0 || r.Offset > len(children) {
			return devtools.Response{Error: "invalid_offset"}
		}
		for i := r.Offset; i < len(children) && i < r.Offset+limit; i++ {
			out.Children = append(out.Children, inspectNode(children[i]))
		}
		if r.Offset+len(out.Children) < len(children) {
			out.Next = r.Offset + len(out.Children)
		}
		return devtools.Response{DOM: out}
	}
	return devtools.Response{Error: "unsupported_method"}
}
func inspectNode(n *dom.Node) devtools.Node {
	out := devtools.Node{ID: uint64(n.ID()), Tag: n.Tag(), Children: len(n.Children())}
	redacted := false
	for p := n; p != nil; p = p.Parent() {
		switch p.Tag() {
		case "script", "style", "textarea", "input":
			redacted = true
		}
	}
	if n.Kind() == dom.Text {
		out.Tag = "#text"
		if redacted {
			out.Text = "[redacted]"
		} else {
			out.Text = devtools.Clip(n.Text(), 256)
		}
	}
	for _, key := range []string{"id", "class", "type", "name", "role", "title", "alt", "href", "src", "style"} {
		if v, ok := n.Attr(key); ok {
			if out.Attributes == nil {
				out.Attributes = map[string]string{}
			}
			if key == "src" || key == "href" {
				v = devtools.URL(v)
			}
			out.Attributes[key] = devtools.Clip(v, 256)
		}
	}
	return out
}
