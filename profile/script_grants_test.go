// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package profile

import (
	"strings"
	"testing"
)

func TestScriptGrantProfileAndCanonicalScope(t *testing.T) {
	p, err := Parse([]byte(`{"base":"browse","script-prompts":true,"script-grants":[{"source":"https://PAGE.test:443/","destination":"https://CDN.test:443"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := p.ScriptOrigins("https://page.test/a?x=1")
	if len(got) != 1 || got[0] != "https://cdn.test" {
		t.Fatal(got)
	}
	for _, source := range []string{"http://page.test", "https://other.test", "https://page.test:444"} {
		if len(p.ScriptOrigins(source)) != 0 {
			t.Fatal("scope leak")
		}
	}
	for _, raw := range []string{"https://page.test/path=https://cdn.test", "https://page.test=https://*.cdn.test", "https://page.test=https://u:p@cdn.test", "file:///tmp/a=https://cdn.test", "https://page.test=https://cdn.test:99999"} {
		if _, err := ParseScriptGrant(raw); err == nil {
			t.Fatal(raw)
		}
	}
	if (Profile{}).ScriptPrompts {
		t.Fatal("default prompts open")
	}
	if !strings.Contains(p.Table(nil), "script-grants") {
		t.Fatal("missing effective grant display")
	}
}
