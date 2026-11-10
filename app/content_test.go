// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func gifBytes(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White}
	g := &gif.GIF{Image: []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 4, 4), pal), image.NewPaletted(image.Rect(0, 0, 4, 4), pal)}, Delay: []int{1, 1}}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// contentServer serves one response per path and counts body bytes written.
func contentServer(t *testing.T, written *atomic.Int64, routes map[string][2]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", rt[0])
		w.Header().Set("Content-Length", strconv.Itoa(len(rt[1])))
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		n, _ := w.Write([]byte(rt[1]))
		written.Add(int64(n))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func contentModel(t *testing.T, p profile.Profile, start string) *Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l := loop.New(ctx, loop.Config{Profile: p, Width: 400}, dropSink{})
	go l.Run()
	m := New(l, p, start)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(cssFrame(t, m, loop.FrameMsg{Gen: 1, Result: renderTall(t, `<p>x</p>`)}))
	return m
}

func openProfile(media profile.Media) profile.Profile {
	p := testProfile(nil, true, NavFree)
	p.Media = media
	return p
}

// run navigates and feeds the result back, as Bubble Tea would.
func run(t *testing.T, m *Model, u string) pageLoadedMsg {
	t.Helper()
	msg := m.navigate(u, pushNew, true)().(pageLoadedMsg)
	m.Update(msg)
	return msg
}

func TestHTMLStillLoads(t *testing.T) {
	var w atomic.Int64
	srv := contentServer(t, &w, map[string][2]string{"/": {"text/html; charset=utf-8", "<p>hello</p>"}})
	m := contentModel(t, openProfile(profile.MediaOff), srv.URL)
	msg := run(t, m, srv.URL+"/")
	if msg.err != nil || msg.html != "<p>hello</p>" || m.current == nil || m.media != nil {
		t.Fatalf("%+v", msg)
	}
}

func TestPlainTextIsEscaped(t *testing.T) {
	var w atomic.Int64
	srv := contentServer(t, &w, map[string][2]string{"/t": {"text/plain", "<script>alert(1)</script>"}})
	m := contentModel(t, openProfile(profile.MediaOff), srv.URL)
	msg := run(t, m, srv.URL+"/t")
	if strings.Contains(msg.html, "<script") || !strings.Contains(msg.html, "<pre>") || !strings.Contains(msg.html, "&lt;script") {
		t.Fatalf("%q", msg.html)
	}
}

func TestUnknownKeepsPage(t *testing.T) {
	var w atomic.Int64
	srv := contentServer(t, &w, map[string][2]string{"/z": {"application/zip", strings.Repeat("x", 2048)}})
	m := contentModel(t, openProfile(profile.MediaVideo), srv.URL)
	m.current = mustURL(t, srv.URL+"/here")
	m.history, m.hIndex = []string{srv.URL + "/here"}, 0
	run(t, m, srv.URL+"/z")
	if m.status.text != "application/zip · 2 KB · can't display" || m.current.Path != "/here" || len(m.history) != 1 {
		t.Fatalf("status %q current %v history %v", m.status.text, m.current, m.history)
	}
}

func TestRefusedKindReadsNoBody(t *testing.T) {
	var w atomic.Int64
	big := strings.Repeat("x", 8<<20)
	srv := contentServer(t, &w, map[string][2]string{"/v": {"video/mp4", big}})
	m := contentModel(t, openProfile(profile.MediaAnimation), srv.URL)
	m.current = mustURL(t, srv.URL+"/here")
	run(t, m, srv.URL+"/v")
	if m.status.text != "video is off in this profile" || m.current.Path != "/here" {
		t.Fatalf("status %q", m.status.text)
	}
	if w.Load() >= int64(len(big)) {
		t.Fatal("the whole body was sent; it should have been abandoned")
	}
}

func TestImageShown(t *testing.T) {
	var w atomic.Int64
	srv := contentServer(t, &w, map[string][2]string{"/i": {"image/png", string(pngBytes(t))}})
	m := contentModel(t, openProfile(profile.MediaImage), srv.URL)
	run(t, m, srv.URL+"/i")
	if m.media == nil || m.media.animated || m.current.Path != "/i" || len(m.history) != 1 {
		t.Fatalf("media %+v current %v", m.media, m.current)
	}
}

func TestAnimationShown(t *testing.T) {
	var w atomic.Int64
	srv := contentServer(t, &w, map[string][2]string{"/g": {"image/gif", string(gifBytes(t))}})
	m := contentModel(t, openProfile(profile.MediaAnimation), srv.URL)
	run(t, m, srv.URL+"/g")
	if m.media == nil || !m.media.animated {
		t.Fatalf("media %+v", m.media)
	}
}

func TestImageRefusedUnderMediaOff(t *testing.T) {
	var w atomic.Int64
	srv := contentServer(t, &w, map[string][2]string{"/i": {"image/png", string(pngBytes(t))}})
	m := contentModel(t, openProfile(profile.MediaOff), srv.URL)
	run(t, m, srv.URL+"/i")
	if m.media != nil || m.status.text != "image is off in this profile" {
		t.Fatalf("status %q", m.status.text)
	}
}

func TestBackFromImageRestoresPage(t *testing.T) {
	var w atomic.Int64
	srv := contentServer(t, &w, map[string][2]string{"/": {"text/html", "<p>page</p>"}, "/i": {"image/png", string(pngBytes(t))}})
	m := contentModel(t, openProfile(profile.MediaImage), srv.URL)
	run(t, m, srv.URL+"/")
	run(t, m, srv.URL+"/i")
	shown := m.media
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("Esc did not go back")
	}
	m.Update(m.navigate(m.history[0], 0, true)().(pageLoadedMsg))
	if m.media != nil || !shown.closed || m.current.Path != "/" {
		t.Fatalf("media %+v closed %v", m.media, shown.closed)
	}
}

func TestOverCapByLength(t *testing.T) {
	var w atomic.Int64
	srv := contentServer(t, &w, map[string][2]string{"/i": {"image/png", strings.Repeat("x", 4096)}})
	p := openProfile(profile.MediaImage)
	p.MaxMediaBytes = 100
	m := contentModel(t, p, srv.URL)
	run(t, m, srv.URL+"/i")
	if m.media != nil || m.status.text != "image/png · 4 KB · over the media limit" {
		t.Fatalf("status %q", m.status.text)
	}
}

func TestOverCapByBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.(http.Flusher).Flush() // chunked: no Content-Length
		w.Write(make([]byte, 4096))
	}))
	defer srv.Close()
	p := openProfile(profile.MediaImage)
	p.MaxMediaBytes = 100
	m := contentModel(t, p, srv.URL)
	run(t, m, srv.URL+"/i")
	if m.media != nil || !strings.Contains(m.status.text, "over the media limit") {
		t.Fatalf("status %q", m.status.text)
	}
}
