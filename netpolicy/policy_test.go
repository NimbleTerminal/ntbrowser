// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package netpolicy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDeniesEverythingOffPolicy(t *testing.T) {
	origin, _ := url.Parse("https://docs.example.com/guide/")
	f := New(Policy{Origin: origin, AllowHosts: []string{".cdn.example.net"}})
	for _, ref := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "data:text/html,hi",
		"http://localhost/", "http://127.0.0.1:8080/", "http://10.0.0.5/", "http://169.254.169.254/latest/meta-data",
		"http://[::1]/", "http://metadata.internal/", "https://user:pw@docs.example.com/",
		"https://other.example.com/", "https://evil.cdn.example.net.attacker.com/", "https://docs.example.com/x y",
	} {
		if _, err := f.Resolve(ref); !errors.Is(err, ErrDenied) && err == nil {
			t.Errorf("%s: expected denial, got %v", ref, err)
		}
	}
	for _, ref := range []string{"/api/data.json", "../sibling.html", "https://docs.example.com/other", "https://img.cdn.example.net/a.json"} {
		if _, err := f.Resolve(ref); err != nil {
			t.Errorf("%s: expected allow, got %v", ref, err)
		}
	}
}

func TestBoundedBodyAndContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(strings.Repeat("x", 5000)))
		case "/bin":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte{0, 1, 2})
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Set-Cookie", "secret=1")
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()
	origin, _ := url.Parse(srv.URL)
	f := New(Policy{Origin: origin, AllowPrivate: true, MaxBody: 4096})
	if _, err := f.Do(context.Background(), Request{URL: "/big"}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big: %v", err)
	}
	if _, err := f.Do(context.Background(), Request{URL: "/bin"}); !errors.Is(err, ErrDenied) {
		t.Errorf("bin: %v", err)
	}
	resp, err := f.Do(context.Background(), Request{URL: "/ok", Headers: map[string]string{"Cookie": "x=1", "Accept": "application/json"}})
	if err != nil || resp.Status != 200 || resp.Body != `{"ok":true}` {
		t.Fatalf("ok: %+v %v", resp, err)
	}
	if _, leaked := resp.Headers["set-cookie"]; leaked {
		t.Error("Set-Cookie leaked to the page")
	}
}

func TestSameOriginIncludesPort(t *testing.T) {
	origin, _ := url.Parse("https://host.example/")
	f := New(Policy{Origin: origin})
	if _, err := f.Resolve("https://host.example:8443/x"); !errors.Is(err, ErrDenied) {
		t.Fatalf("port differs, expected denial: %v", err)
	}
	if _, err := f.Resolve("https://host.example:443/x"); err != nil {
		t.Fatalf("default port should match: %v", err)
	}
}

// TestImageBodyCapIsSeparate: MaxImageBody raises the download cap for image
// requests only; stylesheets, scripts and documents keep MaxBody.
func TestImageBodyCapIsSeparate(t *testing.T) {
	payload := strings.Repeat("x", 6000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".png"):
			w.Header().Set("Content-Type", "image/png")
		case strings.HasSuffix(r.URL.Path, ".css"):
			w.Header().Set("Content-Type", "text/css")
		default:
			w.Header().Set("Content-Type", "text/html")
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()
	origin, _ := url.Parse(srv.URL)
	f := New(Policy{Origin: origin, AllowPrivate: true, MaxBody: 4096, MaxImageBody: 8192})
	ctx := context.Background()
	if resp, err := f.Do(ctx, Request{URL: srv.URL + "/a.png", Kind: KindImage}); err != nil || len(resp.Body) != len(payload) {
		t.Fatalf("image under its own cap: %v", err)
	}
	for _, path := range []string{"/a.css", "/page.html"} {
		kind := KindStyle
		if path == "/page.html" {
			kind = KindNavigate
		}
		var limit *BodyLimitError
		if _, err := f.Do(ctx, Request{URL: srv.URL + path, Kind: kind}); !errors.As(err, &limit) || limit.Limit != 4096 {
			t.Fatalf("%s should keep MaxBody: %v", path, err)
		}
	}
	plain := New(Policy{Origin: origin, AllowPrivate: true, MaxBody: 4096})
	var limit *BodyLimitError
	if _, err := plain.Do(ctx, Request{URL: srv.URL + "/a.png", Kind: KindImage}); !errors.As(err, &limit) || limit.Limit != 4096 || limit.Resource != "image" {
		t.Fatalf("without MaxImageBody images keep MaxBody: %v", err)
	}
}
