// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package netpolicy

import (
	"net/url"
	"testing"
)

func TestScriptPolicyIsolatedFromFetchHosts(t *testing.T) {
	base, _ := url.Parse("https://page.test/")
	fetch := New(Policy{Origin: base, AllowHosts: []string{"cdn.test"}, AnyOrigin: true})
	scripts := fetch.ForScripts(base.String(), nil)
	if _, err := scripts.Resolve("https://cdn.test/s.js"); err == nil {
		t.Fatal("shared allowlist or AnyOrigin leaked")
	}
	granted := fetch.ForScripts(base.String(), []string{"https://cdn.test"})
	if _, err := granted.Resolve("https://cdn.test/s.js"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://cdn.test:444/s.js", "http://cdn.test/s.js", "https://sub.cdn.test/s.js"} {
		if _, err := granted.Resolve(raw); err == nil {
			t.Fatal("grant widened", raw)
		}
	}
	if scripts.sem != fetch.sem || granted.sem != fetch.sem {
		t.Fatal("request budgets forked")
	}
	if _, err := New(Policy{}).ForScripts(base.String(), []string{"https://cdn.test"}).Resolve("https://cdn.test/s.js"); err == nil {
		t.Fatal("zero policy opened")
	}
}
