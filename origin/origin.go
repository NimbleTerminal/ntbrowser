// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package origin

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// Of returns an exact HTTP(S) scheme/host/effective-port origin. Grant syntax
// deliberately accepts ASCII DNS names and IP literals, not wildcard domains.
func Of(raw string) (string, error) {
	if len(raw) > 8192 {
		return "", fmt.Errorf("URL too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || !httpScheme(u.Scheme) || u.Hostname() == "" {
		return "", fmt.Errorf("expected HTTP(S) origin")
	}
	host := strings.ToLower(u.Hostname())
	if len(host) > 253 {
		return "", fmt.Errorf("origin hostname too long")
	}
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == ':') {
			return "", fmt.Errorf("origin must use an ASCII hostname or IP literal")
		}
	}
	canonical, ok := OfURL(u)
	if !ok {
		return "", fmt.Errorf("invalid origin port")
	}
	return canonical, nil
}

// OfURL serializes the origin of a parsed URL: lower-case scheme and host,
// the port only when it is not the scheme's default. It reports false for a
// URL with no tuple origin here: nil, not HTTP(S), carrying credentials,
// without a host, or with a host or port that cannot be canonicalized.
// Non-ASCII hosts compare by their IDNA lookup form, the name actually
// requested, so a Unicode name and its punycode spelling are one origin.
func OfURL(u *url.URL) (string, bool) {
	if u == nil || u.User != nil || !httpScheme(u.Scheme) {
		return "", false
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
	if strings.IndexFunc(host, func(r rune) bool { return r >= 0x80 }) >= 0 {
		ascii, err := idna.Lookup.ToASCII(host)
		if err != nil || ascii == "" {
			return "", false
		}
		host = ascii
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
		port = strconv.Itoa(n)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" && !(scheme == "https" && port == "443") && !(scheme == "http" && port == "80") {
		host += ":" + port
	}
	return scheme + "://" + host, true
}

// Same reports whether two URLs share an origin: scheme, host and effective
// port. It is the one same-origin predicate; a URL without an origin (see
// OfURL) is same-origin with nothing, itself included.
func Same(a, b *url.URL) bool {
	x, ok := OfURL(a)
	if !ok {
		return false
	}
	y, ok := OfURL(b)
	return ok && x == y
}

func httpScheme(s string) bool {
	return strings.EqualFold(s, "https") || strings.EqualFold(s, "http")
}

func Parse(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("grant must name an origin, without path, query or fragment")
	}
	return Of(raw)
}
