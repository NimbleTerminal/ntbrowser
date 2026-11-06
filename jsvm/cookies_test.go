// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import "testing"

func TestDocumentCookieBinding(t *testing.T) {
	s := dynamicSandbox(t, false)
	imageEval(t, s, `document.cookie="x=1"`)
	if got := evalString(t, s, `document.cookie`); got != "" {
		t.Fatal("default gate open", got)
	}
	value := "server=1"
	s.cfg.CookieRead = func() string { return value }
	s.cfg.CookieWrite = func(v string) { value = v }
	if got := evalString(t, s, `document.cookie`); got != "server=1" {
		t.Fatal(got)
	}
	imageEval(t, s, `document.cookie={toString(){return "client=2"}}`)
	if value != "client=2" {
		t.Fatal(value)
	}
}

type credentialHost struct {
	nopHost
	requests []FetchRequest
}

func (h *credentialHost) Fetch(r FetchRequest, _ func(FetchResponse, error)) error {
	h.requests = append(h.requests, r)
	return nil
}
func TestFetchCredentialsAreForwarded(t *testing.T) {
	s := dynamicSandbox(t, false)
	h := &credentialHost{}
	s.host = h
	imageEval(t, s, `fetch("/a",{credentials:"omit"});fetch("/b");fetch("/c",{credentials:"include"});let invalid=false;fetch("/d",{credentials:"invalid"}).catch(e=>invalid=e instanceof TypeError);`)
	if len(h.requests) != 3 || !h.requests[0].OmitCookies || h.requests[1].OmitCookies || h.requests[2].OmitCookies {
		t.Fatal(h.requests)
	}
	if evalString(t, s, `invalid`) != "true" {
		t.Fatal("invalid credentials not rejected")
	}
}
