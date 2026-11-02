// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package netpolicy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// kindServer answers by path and records the Accept header each path saw.
type kindServer struct {
	*httptest.Server
	mu     sync.Mutex
	accept map[string]string
}

func newKindServer(t *testing.T) *kindServer {
	t.Helper()
	ks := &kindServer{accept: map[string]string{}}
	ks.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ks.mu.Lock()
		ks.accept[r.URL.Path] = r.Header.Get("Accept")
		ks.mu.Unlock()
		switch r.URL.Path {
		case "/doc.json":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
		case "/missing.html":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(404)
			w.Write([]byte("<h1>not found</h1>"))
		case "/ok.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			w.Write([]byte("h1{color:red}"))
		case "/moved.css":
			http.Redirect(w, r, "/ok.css", http.StatusFound)
		case "/soft404.css":
			w.Header().Set("Content-Type", "text/css")
			w.WriteHeader(404)
			w.Write([]byte("h1{color:red}"))
		case "/page.css":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>interstitial</html>"))
		case "/raw.css":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte("h1{color:blue}"))
		case "/raw.txt":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte("h1{color:blue}"))
		case "/p.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("PNG-BYTES"))
		case "/moved.png":
			http.Redirect(w, r, "/p.png", http.StatusFound)
		case "/i.ico":
			w.Header().Set("Content-Type", "image/x-icon")
			w.Write([]byte("ICO"))
		case "/broken.png":
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(500)
			w.Write([]byte("PNG-BYTES"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(ks.Close)
	return ks
}

func (ks *kindServer) seen(path string) string {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.accept[path]
}

func kindFetcher(t *testing.T, ks *kindServer) *Fetcher {
	t.Helper()
	origin, err := url.Parse(ks.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	return New(Policy{Origin: origin, AllowPrivate: true})
}

func TestKindDocumentIsUnchanged(t *testing.T) {
	ks := newKindServer(t)
	f := kindFetcher(t, ks)
	ctx := context.Background()
	resp, err := f.Do(ctx, Request{Method: "GET", URL: "/doc.json"})
	if err != nil || resp.Body != `{"ok":true}` {
		t.Fatalf("document fetch: %v %q", err, resp.Body)
	}
	if got := ks.seen("/doc.json"); got != "text/*, application/json;q=0.9" {
		t.Fatalf("document Accept changed: %q", got)
	}
	resp, err = f.Do(ctx, Request{Method: "GET", URL: "/missing.html"})
	if err != nil || resp.Status != 404 || !strings.Contains(resp.Body, "not found") {
		t.Fatalf("document 404 must be returned to fetch(): %v %+v", err, resp)
	}
}

func TestKindStyleAcceptAndGate(t *testing.T) {
	ks := newKindServer(t)
	f := kindFetcher(t, ks)
	ctx := context.Background()
	resp, err := f.Do(ctx, Request{Method: "GET", URL: "/ok.css", Kind: KindStyle})
	if err != nil || resp.Body != "h1{color:red}" {
		t.Fatalf("text/css; charset=utf-8 must be accepted: %v %q", err, resp.Body)
	}
	if got := ks.seen("/ok.css"); got != "text/css" {
		t.Fatalf("style Accept: %q", got)
	}
	resp, err = f.Do(ctx, Request{Method: "GET", URL: "/moved.css", Kind: KindStyle})
	if err != nil || resp.Body != "h1{color:red}" || !strings.HasSuffix(resp.URL, "/ok.css") {
		t.Fatalf("302 to a stylesheet must be followed: %v %+v", err, resp)
	}
	for _, path := range []string{"/soft404.css", "/page.css", "/raw.txt"} {
		if _, err := f.Do(ctx, Request{Method: "GET", URL: path, Kind: KindStyle}); !errors.Is(err, ErrDenied) {
			t.Fatalf("%s must be refused for KindStyle, got %v", path, err)
		}
	}
	resp, err = f.Do(ctx, Request{Method: "GET", URL: "/raw.css", Kind: KindStyle})
	if err != nil || resp.Body != "h1{color:blue}" {
		t.Fatalf("application/octet-stream with a .css path must be accepted: %v", err)
	}
}

func TestKindImageAcceptAndGate(t *testing.T) {
	ks := newKindServer(t)
	f := kindFetcher(t, ks)
	ctx := context.Background()
	resp, err := f.Do(ctx, Request{Method: "GET", URL: "/p.png", Kind: KindImage})
	if err != nil || resp.Body != "PNG-BYTES" {
		t.Fatalf("image/png must be accepted: %v %q", err, resp.Body)
	}
	if got := ks.seen("/p.png"); got != "image/png, image/jpeg, image/gif, image/webp, image/svg+xml" {
		t.Fatalf("image Accept: %q", got)
	}
	resp, err = f.Do(ctx, Request{Method: "GET", URL: "/moved.png", Kind: KindImage})
	if err != nil || resp.Body != "PNG-BYTES" {
		t.Fatalf("302 to an image must be followed: %v", err)
	}
	for _, path := range []string{"/i.ico", "/broken.png"} {
		if _, err := f.Do(ctx, Request{Method: "GET", URL: path, Kind: KindImage}); !errors.Is(err, ErrDenied) {
			t.Fatalf("%s must be refused for KindImage, got %v", path, err)
		}
	}
}

func TestNoDowngradeFromHTTPS(t *testing.T) {
	origin, _ := url.Parse("https://example.test/")
	// AnyOrigin everywhere below so the only rule that can refuse is the
	// downgrade check (an http URL is never same-origin with an https page).
	f := New(Policy{Origin: origin, NoDowngradeFrom: "https", AllowPrivate: true, AnyOrigin: true})
	insecure, _ := url.Parse("http://example.test/a.css")
	secure, _ := url.Parse("https://example.test/a.css")
	if err := f.Allowed(insecure); !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "mixed content") {
		t.Fatalf("http under an https page must be refused as mixed content, got %v", err)
	}
	if err := f.Allowed(secure); err != nil {
		t.Fatalf("https under an https page: %v", err)
	}
	for _, from := range []string{"http", ""} {
		f := New(Policy{Origin: origin, NoDowngradeFrom: from, AllowPrivate: true, AnyOrigin: true})
		if err := f.Allowed(insecure); err != nil {
			t.Fatalf("NoDowngradeFrom %q must allow http: %v", from, err)
		}
	}
}
