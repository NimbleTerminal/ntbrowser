// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package netpolicy

import (
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBodyLimitReportsDecodedSizeAndFinalResource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/large.js?token=secret", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Content-Encoding", "gzip")
		z := gzip.NewWriter(w)
		_, _ = z.Write([]byte(strings.Repeat("x", 2048)))
		_ = z.Close()
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	f := New(Policy{Origin: u, AllowPrivate: true, MaxBody: 1024})
	_, err := f.Do(context.Background(), Request{URL: "/redirect", Kind: KindScript})
	var detail *BodyLimitError
	if !errors.Is(err, ErrTooLarge) || !errors.As(err, &detail) {
		t.Fatalf("error %v", err)
	}
	if detail.Limit != 1024 || detail.Observed != 1025 || !detail.AtLeast || detail.Resource != "script" || detail.URL != srv.URL+"/large.js?token=secret" {
		t.Fatalf("details %+v", detail)
	}
	if strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "script limit 1024 bytes") || !strings.Contains(err.Error(), "/large.js") {
		t.Fatalf("message %s", err)
	}
}
func TestRequestLimitIdentifiesRequestBody(t *testing.T) {
	u, _ := url.Parse("https://example.test/")
	f := New(Policy{Origin: u, MaxBody: 4})
	_, err := f.Do(context.Background(), Request{Method: "POST", URL: "/submit", Body: "12345"})
	var detail *BodyLimitError
	if !errors.Is(err, ErrTooLarge) || !errors.As(err, &detail) || !detail.Request || detail.AtLeast || detail.Observed != 5 || !strings.Contains(err.Error(), "request body too large") {
		t.Fatalf("error %v", err)
	}
}
