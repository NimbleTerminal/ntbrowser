// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package origin

import (
	"net/url"
	"testing"
)

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

func TestSame(t *testing.T) {
	u := func(raw string) *url.URL {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(raw, err)
		}
		return parsed
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"https://example.test/a", "https://example.test/b?q#f", true},
		{"https://EXAMPLE.test/", "https://example.test:443/", true},
		{"http://example.test/", "http://example.test:80/", true},
		{"https://example.test:0443/", "https://example.test/", true},
		{"http://[::1]:8080/", "http://[0:0:0:0:0:0:0:1]:8080/x", false}, // literal comparison, like Of
		{"http://[::1]:8080/", "http://[::1]:8080/x", true},
		{"https://bücher.test/", "https://xn--bcher-kva.test/", true},
		{"https://a_b.example.test/", "https://a_b.example.test:443/x", true},
		{"https://example.test/", "http://example.test/", false},
		{"https://example.test/", "https://example.test:444/", false},
		{"http://example.test:443/", "https://example.test/", false},
		{"https://example.test/", "https://www.example.test/", false},
		{"https://example.test/", "https://example.test./", false},
		{"https://user@example.test/", "https://example.test/", false},
		{"https://user@example.test/", "https://user@example.test/", false},
		{"file:///a", "file:///b", false},
		{"about:blank", "about:blank", false},
		{"https:///path", "https:///path", false},
	} {
		if got := Same(u(c.a), u(c.b)); got != c.want {
			t.Errorf("Same(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
		if got := Same(u(c.b), u(c.a)); got != c.want {
			t.Errorf("Same(%s, %s) = %v, want %v", c.b, c.a, got, c.want)
		}
	}
	if Same(nil, u("https://example.test/")) || Same(nil, nil) {
		t.Error("a nil URL has no origin")
	}
}

func TestOfURLMatchesOf(t *testing.T) {
	for _, raw := range []string{"https://EXAMPLE.test:443/path", "http://example.test:80/", "https://example.test:444/", "http://[::1]:8080/x"} {
		parsed, _ := url.Parse(raw)
		want, _ := Of(raw)
		if got, ok := OfURL(parsed); !ok || got != want {
			t.Errorf("OfURL(%s) = %q, %v; want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"file:///tmp/a", "https://user@example.test/", "about:blank"} {
		parsed, _ := url.Parse(raw)
		if got, ok := OfURL(parsed); ok {
			t.Errorf("OfURL(%s) = %q, want no origin", raw, got)
		}
	}
}
