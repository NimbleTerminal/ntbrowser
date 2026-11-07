// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package profile

import "testing"

func TestFrameGrantsSeparateAndNormalized(t *testing.T) {
	p := Profile{Frames: FramesAllowlist, FrameGrants: []ScriptGrant{{"https://EXAMPLE.test:443", "https://CHILD.test:443"}}}
	got := p.FrameOrigins("https://example.test/path")
	if len(got) != 1 || got[0] != "https://child.test" {
		t.Fatal(got)
	}
	if len(p.ScriptOrigins("https://example.test")) != 0 {
		t.Fatal("script authority leak")
	}
	p.Frames = FramesOrigin
	if len(p.FrameOrigins("https://example.test")) != 0 {
		t.Fatal("origin mode uses grant")
	}
	if (Profile{}).FramesOn() {
		t.Fatal("zero profile open")
	}
}
