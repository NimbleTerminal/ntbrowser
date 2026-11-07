// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package netpolicy

import (
	"net/url"
	"testing"
)

func TestFramePoliciesKeepBoundaries(t *testing.T) {
	u, _ := url.Parse("https://parent.test")
	f := New(Policy{Origin: u, AnyOrigin: true, AllowHosts: []string{"fetch.test"}})
	child := f.ForFrame(u.String(), []string{"https://child.test"})
	if child.sem != f.sem || child.p.AnyOrigin || len(child.p.AllowHosts) != 0 || child.p.MaxBody != 256<<10 || child.p.Cookies != nil {
		t.Fatal("frame policy widened")
	}
	page := child.ForFramePage("https://child.test/page", nil, false)
	if page.sem != f.sem || len(page.p.AllowOrigins) != 0 || page.p.Origin.Host != "child.test" || page.p.Cookies != nil {
		t.Fatal("child inherited frame grants")
	}
	closed := New(Policy{}).ForFramePage(u.String(), nil, true)
	if closed.p.Origin != nil {
		t.Fatal("closed policy reopened")
	}
	if acceptableFor(KindFrame, "/child", "image/png") == nil {
		t.Fatal("bad MIME accepted")
	}
}
