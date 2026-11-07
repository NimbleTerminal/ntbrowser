// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"strings"
	"testing"
)

func TestFrameSerializationKeepsDOMAndRasterAuthoritySeparate(t *testing.T) {
	tree, err := Parse(strings.NewReader(`<iframe id=f class=child src="https://untrusted.test/" srcset="https://untrusted.test/other" srcdoc="private child source" style="border:0"></iframe>`), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	n := tree.ByID("f")
	rendered := tree.SerializeFrames(map[NodeID]FrameImage{n.ID(): {Source: "ntbrowser-frame-2", Width: 300, Height: 150}})
	if !strings.Contains(rendered, `<img class="child"`) || !strings.Contains(rendered, `src="ntbrowser-frame-2"`) || strings.Contains(rendered, "untrusted") || strings.Contains(rendered, "private child") {
		t.Fatal(rendered)
	}
	if n.Tag() != "iframe" {
		t.Fatal("renderer mutated DOM")
	}
	if !strings.Contains(tree.Serialize(), "untrusted.test") {
		t.Fatal("frame substitution escaped serialization")
	}
	if len(rendered) > tree.limits.TotalBytes {
		t.Fatal("serialization budget")
	}
}
