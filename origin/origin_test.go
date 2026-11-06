// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package origin

import "testing"

func TestExactOriginCanonicalization(t *testing.T) {
	for raw, want := range map[string]string{"https://EXAMPLE.test:443/path": "https://example.test", "http://example.test:80/": "http://example.test", "https://example.test:444/": "https://example.test:444", "http://[::1]:8080/x": "http://[::1]:8080"} {
		got, err := Of(raw)
		if err != nil || got != want {
			t.Fatal(raw, got, err)
		}
	}
	for _, raw := range []string{"https://example.test/path", "https://example.test?q=1", "https://example.test#fragment", "https://*.test", "https://user@example.test", "file:///tmp/a"} {
		if _, err := Parse(raw); err == nil {
			t.Fatal("invalid grant origin", raw)
		}
	}
}
