// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/diagnostics"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
)

func TestPageBodyLimitDiagnostic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
	}))
	defer srv.Close()
	p, _ := profile.Builtin("browse")
	f := netpolicy.New(netpolicy.Policy{AnyOrigin: true, AllowPrivate: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := load(ctx, cancel, f, srv.URL+"/search?q=private", p, 1<<20, nil, pageLoadedMsg{})
	var detail *netpolicy.BodyLimitError
	if !errors.Is(result.err, netpolicy.ErrTooLarge) || !errors.As(result.err, &detail) {
		t.Fatalf("error %v", result.err)
	}
	if detail.Resource != "page" || detail.Limit != 1<<20 || detail.Observed != (1<<20)+1 || !detail.AtLeast || result.html != "" {
		t.Fatalf("details %+v", detail)
	}
	if strings.Contains(result.err.Error(), "private") || !strings.Contains(result.err.Error(), "page limit 1048576 bytes") {
		t.Fatal(result.err)
	}
}
func TestReadCappedExactBoundary(t *testing.T) {
	b, err := readCapped(strings.NewReader("1234"), 4)
	if err != nil || string(b) != "1234" {
		t.Fatalf("boundary %q %v", b, err)
	}
}

func TestNavigationBodyFailureIsCaptured(t *testing.T) {
	var output bytes.Buffer
	recorder := diagnostics.New(&output)
	m := &Model{profile: profile.Profile{Diagnostics: true}, navSeq: 1}
	m.SetDiagnostics(recorder)
	m.Update(pageLoadedMsg{seq: 1, finalURL: "https://example.test/search", err: &netpolicy.BodyLimitError{Resource: "page", URL: "https://example.test/search", Limit: 1024, Observed: 1025, AtLeast: true}})
	recorder.Close()
	if !strings.Contains(output.String(), `"kind":"navigation_error"`) || !strings.Contains(output.String(), "page limit 1024 bytes") {
		t.Fatalf("capture %s", output.String())
	}
}
