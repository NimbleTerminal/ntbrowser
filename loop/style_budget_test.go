// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/nthtml/document"
)

func TestHostStylesMatchRendererBudget(t *testing.T) {
	l := New(context.Background(), Config{DOMLimits: dom.Limits{TotalBytes: 1024, ExternalStyleBytes: 8192}}, nil)
	if l.cfg.RenderLimit.Bytes != 9216 {
		t.Fatalf("renderer cap: %d", l.cfg.RenderLimit.Bytes)
	}
	tree, err := dom.Parse(strings.NewReader(`<link rel="stylesheet" href="a.css"><p>text</p>`), l.cfg.DOMLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.SetStyle("a.css", "/*"+strings.Repeat("x", 4096)+"*/p{color:red}"); err != nil {
		t.Fatal(err)
	}
	if _, err := document.Parse(context.Background(), strings.NewReader(tree.Serialize()), l.cfg.RenderLimit); err != nil {
		t.Fatal(err)
	}
	limited := New(context.Background(), Config{DOMLimits: dom.Limits{TotalBytes: 1024, ExternalStyleBytes: 8192}, RenderLimit: document.Limits{Bytes: 2048}}, nil)
	if limited.cfg.DOMLimits.ExternalStyleBytes != 1024 {
		t.Fatal("explicit renderer ceiling widened")
	}
}
