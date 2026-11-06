// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package netpolicy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/cookies"
)

func TestSessionCookieRoundTripAndOmit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if r.URL.Path == "/set" {
			w.Header().Add("Set-Cookie", "server=yes; HttpOnly; Path=/")
			w.Header().Add("Set-Cookie", "visible=yes; Path=/")
		}
		w.Write([]byte(r.Header.Get("Cookie")))
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	store := cookies.New()
	f := New(Policy{Origin: u, AllowPrivate: true, Cookies: store})
	response, err := f.Do(context.Background(), Request{URL: "/set"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Headers["set-cookie"] != "" {
		t.Fatal("Set-Cookie exposed")
	}
	if got := store.Document(server.URL); got != "visible=yes" {
		t.Fatal(got)
	}
	f.SetDocumentCookie(server.URL, "script=yes; Path=/")
	response, err = f.Do(context.Background(), Request{URL: "/echo", Headers: map[string]string{"Cookie": "forged=yes"}})
	if err != nil || !strings.Contains(response.Body, "server=yes") || !strings.Contains(response.Body, "script=yes") || strings.Contains(response.Body, "forged") {
		t.Fatal(response, err)
	}
	response, err = f.Do(context.Background(), Request{URL: "/set", OmitCookies: true})
	if err != nil || response.Body != "" {
		t.Fatal(response, err)
	}
	closed := New(Policy{Origin: u, AllowPrivate: true})
	response, err = closed.Do(context.Background(), Request{URL: "/echo"})
	if err != nil || response.Body != "" {
		t.Fatal("gate off", response, err)
	}
}
func TestCrossOriginSubrequestsStayCookieless(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Set-Cookie", "third=bad; Path=/")
		w.Write([]byte(r.Header.Get("Cookie")))
	}))
	defer other.Close()
	u, _ := url.Parse("http://127.0.0.1:1/")
	store := cookies.New()
	store.SetDocument(other.URL, "existing=secret; Path=/")
	f := New(Policy{Origin: u, AnyOrigin: true, AllowPrivate: true, Cookies: store})
	response, err := f.Do(context.Background(), Request{URL: other.URL})
	if err != nil || response.Body != "" || strings.Contains(store.Document(other.URL), "third=") {
		t.Fatal(response, err)
	}
	response, err = f.Do(context.Background(), Request{URL: other.URL, Kind: KindNavigate})
	if err != nil || response.Body != "existing=secret" {
		t.Fatal("navigation cookies missing", response, err)
	}
}

func TestRedirectDoesNotLeakCookiesOrBypassOmit(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Set-Cookie", "destination=1; Path=/")
		w.Write([]byte(r.Header.Get("Cookie")))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer source.Close()
	origin, _ := url.Parse(source.URL)
	store := cookies.New()
	store.SetDocument(source.URL, "private=1; Path=/")
	f := New(Policy{Origin: origin, AnyOrigin: true, AllowPrivate: true, Cookies: store})
	for _, req := range []Request{{URL: source.URL}, {URL: source.URL, Kind: KindNavigate, OmitCookies: true}} {
		response, err := f.Do(context.Background(), req)
		if err != nil || response.Body != "" || strings.Contains(store.Document(source.URL), "destination=") {
			t.Fatal(response, err)
		}
	}
}
