// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/devtools"
	"nimbleterminal.dev/ntbrowser/dom"
)

func TestInspectionGateSnapshotAndStaleDOM(t *testing.T) {
	p := testProfile
	l, _, _ := scriptTestLoop(t, p, "https://example.test")
	if out, err := l.Inspect(context.Background(), devtools.Request{Method: "snapshot"}); err != nil || out.Error != "disabled" {
		t.Fatal(out, err)
	}
	p.DevTools = true
	l, _, f := scriptTestLoop(t, p, "https://example.test")
	l.LoadPage("<p>Hello</p>", "https://example.test/?token=SECRET", f)
	pageState(t, l)
	s := devtools.New(true, l.Inspect)
	req := devtools.Request{Version: 1, Session: s.ID(), Method: "snapshot"}
	out := s.Do(context.Background(), req)
	if out.Error != "" || len(out.Snapshot.Frames) != 1 || strings.Contains(out.Snapshot.Frames[0].URL, "SECRET") {
		t.Fatal(out)
	}
	rev := out.Snapshot.Frames[0].Revision
	req.Method = "dom"
	req.Revision = rev
	req.Limit = 1
	first := s.Do(context.Background(), req)
	if first.Error != "" || first.DOM == nil || len(first.DOM.Children) != 1 {
		t.Fatal(first)
	}
	l.LoadPage("<p>Replacement</p>", "https://example.test/new", f)
	pageState(t, l)
	if stale := s.Do(context.Background(), req); stale.Error != "stale_document" {
		t.Fatal("old handle accepted", stale)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req.Method = "snapshot"
	newer := s.Do(ctx, req)
	if newer.Snapshot.Frames[0].Revision == rev {
		t.Fatal("revision reused")
	}
}
func TestInspectionOmitsSensitiveDOMFields(t *testing.T) {
	tree, err := dom.Parse(strings.NewReader(`<input value="SECRET"><textarea>SECRET</textarea><script>SECRET</script><a href="https://user:SECRET@host.test/path?token=SECRET#SECRET" data-token="SECRET">visible</a>`), dom.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var visit func(*dom.Node)
	visit = func(n *dom.Node) {
		b, _ := json.Marshal(inspectNode(n))
		if strings.Contains(string(b), "SECRET") {
			t.Fatal("sensitive field exposed", string(b))
		}
		for _, c := range n.Children() {
			visit(c)
		}
	}
	visit(tree.Root())
}
