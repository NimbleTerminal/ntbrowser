// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package netpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/diagnostics"
)

func TestDiagnosticPreservesRejectedResponseMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(204)
	}))
	defer server.Close()
	var b bytes.Buffer
	trace := diagnostics.New(&b)
	f := New(Policy{AnyOrigin: true, AllowPrivate: true, Trace: trace})
	_, err := f.Do(context.Background(), Request{URL: server.URL, Kind: KindImage})
	if err == nil {
		t.Fatal("image content gate bypassed")
	}
	trace.Close()
	var record struct {
		Kind string
		Data struct {
			Status      int
			ContentType string `json:"content_type"`
			Error       string
		}
	}
	json.Unmarshal(bytes.Split(b.Bytes(), []byte{'\n'})[0], &record)
	if record.Kind != "network" || record.Data.Status != 204 || record.Data.ContentType != "text/html" || !strings.Contains(record.Data.Error, "not a decodable image") {
		t.Fatalf("%s", b.Bytes())
	}
}
