// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/cookies"
	"nimbleterminal.dev/ntbrowser/devtools"
	"nimbleterminal.dev/ntbrowser/diagnostics"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
)

// Options hand the model its collaborators; the frozen profile still decides
// which of them it may use.
func TestOptionsRespectProfileGates(t *testing.T) {
	var out bytes.Buffer
	o := Options{
		Start:       "https://example.test/",
		Cookies:     cookies.New(),
		Diagnostics: diagnostics.New(&out),
		DevTools:    devtools.New(true, func(context.Context, devtools.Request) (devtools.Response, error) { return devtools.Response{}, nil }),
		Pool:        netpolicy.NewPool(),
		PageBytes:   2 << 20,
		PageScale:   2,
		Glynx:       true,
		Unenforced:  true,
	}
	closed := New(nil, profile.Profile{}, o)
	if closed.policy.Cookies != nil || closed.policy.Trace != nil || closed.debug.session != nil {
		t.Fatal("a closed profile accepted cookies, diagnostics or devtools")
	}
	if closed.policy.Pool != o.Pool || closed.start != o.Start || closed.pageBodyLimit() != 2<<20 || closed.pageScale != 2 || !closed.glynx || !closed.notEnforced {
		t.Fatalf("options not applied: %+v", closed)
	}
	open := New(nil, profile.Profile{Cookies: profile.CookiesSession, Diagnostics: true, DevTools: true}, o)
	if open.policy.Cookies != o.Cookies || open.policy.Trace != o.Diagnostics || open.debug.session != o.DevTools {
		t.Fatal("an open profile did not receive its collaborators")
	}
	for _, scale := range []float64{0.5, 4.5, -1} {
		if m := New(nil, profile.Profile{}, Options{PageScale: scale}); m.pageScale != 0 {
			t.Errorf("scale %v kept as %v, want auto", scale, m.pageScale)
		}
	}
}

// The local file reader belongs to one model: with none, file: pages are
// refused, and one model's reader is never another's.
func TestLocalReaderIsPerModel(t *testing.T) {
	load := func(m *Model) pageLoadedMsg {
		return m.navigate("file:///tmp/page.html", pushNew, reachAny)().(pageLoadedMsg)
	}
	with := New(nil, profile.Profile{}, Options{Start: "file:///tmp/page.html", OpenLocal: func(path string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("<p>local " + path + "</p>")), nil
	}})
	without := New(nil, profile.Profile{}, Options{Start: "file:///tmp/page.html"})
	if msg := load(with); msg.err != nil || msg.html != "<p>local /tmp/page.html</p>" {
		t.Fatalf("with a reader: %q %v", msg.html, msg.err)
	}
	if msg := load(without); msg.err == nil || !strings.Contains(msg.err.Error(), "local files disabled") {
		t.Fatalf("without a reader: %q %v", msg.html, msg.err)
	}
}
