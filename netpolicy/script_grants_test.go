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

func TestExplicitAnySourcesRetainNetworkBoundaries(t *testing.T) {
	base, _ := url.Parse("https://page.test/")
	parent := New(Policy{Origin: base})
	for _, derive := range []struct {
		name string
		fn   func(string, []string, bool) *Fetcher
	}{
		{"scripts", parent.ForScriptSources}, {"frames", parent.ForFrameSources},
	} {
		t.Run(derive.name, func(t *testing.T) {
			f := derive.fn(base.String(), nil, true)
			if _, err := f.Resolve("https://cdn.test/resource"); err != nil {
				t.Fatal(err)
			}
			if f.sem != parent.sem {
				t.Fatal("network semaphore forked")
			}
			for _, raw := range []string{"https://localhost/x", "https://127.0.0.1/x", "https://192.168.1.1/x", "https://[::1]/x", "file:///tmp/x", "https://user:pass@cdn.test/x", "http://cdn.test/x"} {
				if _, err := f.Resolve(raw); err == nil {
					t.Fatal("network boundary escaped", raw)
				}
			}
			if _, err := derive.fn(base.String(), nil, false).Resolve("https://cdn.test/x"); err == nil {
				t.Fatal("any mode leaked")
			}
			for _, badBase := range []string{"", "file:///tmp/a", "https://", "https://user:pass@page.test/"} {
				if _, err := derive.fn(badBase, nil, true).Resolve("https://cdn.test/x"); err == nil {
					t.Fatal("invalid base opened policy", badBase)
				}
			}
			child := f.ForFramePage("https://child.test/", nil, false)
			if _, err := child.Resolve("https://cdn.test/x"); err == nil {
				t.Fatal("child fetch inherited admission")
			}
		})
	}
	if _, err := parent.Resolve("https://cdn.test/x"); err == nil {
		t.Fatal("parent fetch widened")
	}
	if _, err := New(Policy{}).ForScriptSources(base.String(), nil, true).Resolve("https://cdn.test/x"); err == nil {
		t.Fatal("zero policy opened")
	}
}
