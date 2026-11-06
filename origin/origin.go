// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package origin

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Of returns an exact HTTP(S) scheme/host/effective-port origin. Grant syntax
// deliberately accepts ASCII DNS names and IP literals, not wildcard domains.
func Of(raw string) (string, error) {
	if len(raw) > 8192 {
		return "", fmt.Errorf("URL too long")
	}
	u, err := url.Parse(raw)
	if err == nil {
		u.Scheme = strings.ToLower(u.Scheme)
	}
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
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
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid origin port")
		}
		port = strconv.Itoa(n)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" && !(u.Scheme == "https" && port == "443") && !(u.Scheme == "http" && port == "80") {
		host += ":" + port
	}
	return u.Scheme + "://" + host, nil
}
func Parse(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("grant must name an origin, without path, query or fragment")
	}
	return Of(raw)
}
