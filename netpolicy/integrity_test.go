// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package netpolicy

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func testDigest(algorithm, body string) string {
	var digest []byte
	switch algorithm {
	case "sha256":
		h := sha256.Sum256([]byte(body))
		digest = h[:]
	case "sha384":
		h := sha512.Sum384([]byte(body))
		digest = h[:]
	case "sha512":
		h := sha512.Sum512([]byte(body))
		digest = h[:]
	}
	return algorithm + "-" + base64.StdEncoding.EncodeToString(digest)
}
func TestIntegrityMetadataAndStrongestDigest(t *testing.T) {
	for _, algorithm := range []string{"sha256", "sha384", "sha512"} {
		raw := testDigest(algorithm, "abc")
		for _, metadata := range []string{raw, strings.TrimRight(raw, "="), strings.ReplaceAll(strings.ReplaceAll(raw, "+", "-"), "/", "_")} {
			parsed, err := parseIntegrity(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if err := parsed.verify("abc"); err != nil {
				t.Fatal(algorithm, err)
			}
			if parsed.verify("abd") == nil {
				t.Fatal("mismatch accepted")
			}
		}
	}
	known, err := parseIntegrity("sha256-ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=")
	if err != nil || known.verify("abc") != nil {
		t.Fatal("known SHA-256 vector", err)
	}
	for _, tc := range []struct {
		metadata string
		pass     bool
	}{
		{testDigest("sha256", "abc") + " " + testDigest("sha512", "wrong"), false},
		{testDigest("sha512", "abc") + " " + testDigest("sha256", "wrong"), true},
		{testDigest("sha384", "wrong") + "\t" + testDigest("sha384", "abc"), true},
		{"unknown-ignored " + testDigest("sha256", "abc"), true},
	} {
		m, err := parseIntegrity(tc.metadata)
		if err != nil {
			t.Fatal(err)
		}
		if (m.verify("abc") == nil) != tc.pass {
			t.Fatal("wrong strongest result", tc.metadata)
		}
	}
	for _, raw := range []string{" ", "unknown-x", "sha512-bad", testDigest("sha256", "abc") + " sha512-bad", testDigest("sha256", "abc") + "?option", strings.Repeat("x", 4097), strings.Repeat("unknown-x ", 33)} {
		if _, err := parseIntegrity(raw); err == nil {
			t.Fatal("invalid metadata admitted", raw)
		}
	}
}

func TestScriptIntegrityTransport(t *testing.T) {
	const body = "window.integrityVerified = true;"
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/javascript")
		if r.URL.Path == "/gzip" {
			w.Header().Set("Content-Encoding", "gzip")
			z := gzip.NewWriter(w)
			fmt.Fprint(z, body)
			z.Close()
			return
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	origin, _ := url.Parse(server.URL)
	f := New(Policy{Origin: origin, AllowPrivate: true})
	for _, path := range []string{"/script", "/gzip"} {
		r, err := f.Script(context.Background(), server.URL+path, testDigest("sha384", body), "none")
		if err != nil || r.Body != body {
			t.Fatal(path, err)
		}
	}
	if r, err := f.Script(context.Background(), server.URL, testDigest("sha384", "wrong"), "none"); err == nil || r.Body != "" {
		t.Fatal("unverified source escaped")
	}
	before := hits.Load()
	for _, tc := range []struct{ metadata, mode string }{{"sha384-bad", "none"}, {testDigest("sha384", body), "use-credentials"}} {
		if _, err := f.Script(context.Background(), server.URL, tc.metadata, tc.mode); err == nil {
			t.Fatal("unsupported accepted")
		}
	}
	if hits.Load() != before {
		t.Fatal("invalid metadata/credential mode reached network")
	}
}

func TestScriptIntegrityCORSAndRedirects(t *testing.T) {
	const body = "void 0;"
	page, _ := url.Parse("http://page.test/")
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("cross-origin redirect was followed") }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://page.test" {
			t.Error("missing controlled Origin header")
		}
		w.Header().Set("Content-Type", "application/javascript")
		switch r.URL.Path {
		case "/allow", "/redirect":
			w.Header().Set("Access-Control-Allow-Origin", "*")
		case "/exact":
			w.Header().Set("Access-Control-Allow-Origin", "http://page.test")
		case "/wrong":
			w.Header().Set("Access-Control-Allow-Origin", "http://other.test")
		case "/duplicate":
			w.Header().Add("Access-Control-Allow-Origin", "*")
			w.Header().Add("Access-Control-Allow-Origin", "*")
		case "/cross":
			w.Header().Set("Access-Control-Allow-Origin", "*")
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/allow", http.StatusFound)
			return
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	f := New(Policy{Origin: page, AllowPrivate: true, AnyOrigin: true})
	digest := testDigest("sha256", body)
	for _, tc := range []struct {
		path, mode string
		allow      bool
	}{{"/allow", "anonymous", true}, {"/exact", "anonymous", true}, {"/redirect", "anonymous", true}, {"/missing", "anonymous", false}, {"/wrong", "anonymous", false}, {"/duplicate", "anonymous", false}, {"/cross", "anonymous", false}, {"/allow", "none", false}} {
		r, err := f.Script(context.Background(), server.URL+tc.path, digest, tc.mode)
		if (err == nil) != tc.allow {
			t.Fatalf("%+v: %v", tc, err)
		}
		if err != nil && r.Body != "" {
			t.Fatal("refused source escaped")
		}
	}
	closed := New(Policy{Origin: page, AllowPrivate: true})
	if _, err := closed.Script(context.Background(), server.URL+"/allow", digest, "anonymous"); err == nil {
		t.Fatal("integrity bypassed origin gate")
	}
}
