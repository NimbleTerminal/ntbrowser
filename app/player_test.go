// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"errors"
	"image"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntmediabox"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
)

type stubDecoder struct {
	mu      sync.Mutex
	ops     []string
	state   ntmediabox.State
	request ntmediabox.OpenRequest
}

func (d *stubDecoder) add(s string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ops = append(d.ops, s)
	return nil
}
func (d *stubDecoder) Load(req ntmediabox.OpenRequest) error {
	d.mu.Lock()
	d.state = ntmediabox.Paused
	d.request = req
	d.mu.Unlock()
	return d.add("load")
}
func (d *stubDecoder) Play() error {
	d.mu.Lock()
	d.state = ntmediabox.Playing
	d.mu.Unlock()
	return d.add("play")
}
func (d *stubDecoder) Pause() error                     { return d.add("pause") }
func (d *stubDecoder) Seek(t time.Duration) error       { return d.add("seek " + t.String()) }
func (d *stubDecoder) SelectTrack(string, string) error { return nil }
func (d *stubDecoder) SetCaptionOverlay(bool) error     { return nil }
func (d *stubDecoder) Status() (ntmediabox.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ntmediabox.Status{State: d.state, Position: 12 * time.Second, Duration: 32 * time.Second}, nil
}
func (d *stubDecoder) Frame(w, h int) ([]byte, int, int, error) {
	return make([]byte, 2*2*4), 2, 2, nil
}
func (d *stubDecoder) Close() error { return nil }

func (d *stubDecoder) saw(s string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, o := range d.ops {
		if o == s {
			return true
		}
	}
	return false
}

type videoRig struct {
	m    *Model
	d    *stubDecoder
	msgs chan tea.Msg
	dir  string
	end  net.Conn
}

func videoModel(t *testing.T, srvURL string) *videoRig {
	t.Helper()
	p := openProfile(profile.MediaVideo)
	p.NativeCode, p.Files = true, profile.FilesTemp
	m := contentModel(t, p, srvURL)
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	d := &stubDecoder{}
	go ntmediabox.Serve(b, func(string) (ntmediabox.Decoder, error) { return d, nil })
	dir := t.TempDir()
	msgs := make(chan tea.Msg, 256)
	bridge, err := NewMediaBridge(a, func(msg tea.Msg) {
		select {
		case msgs <- msg:
		default:
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.SetMedia(bridge, dir)
	return &videoRig{m: m, d: d, msgs: msgs, dir: dir, end: b}
}

func (r *videoRig) pump(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.After(patience)
	for !done() {
		select {
		case msg := <-r.msgs:
			r.m.Update(msg)
		case <-deadline:
			t.Fatal("timed out")
		}
	}
}

func dirEntries(t *testing.T, dir string) int {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

func videoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(make([]byte, 2048))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVideoLinkPlays(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	run(t, r.m, srv.URL+"/v.mp4")
	if r.m.player == nil {
		t.Fatalf("no player; status %q", r.m.status.text)
	}
	if len(r.m.history) != 0 {
		t.Fatalf("a video is not a history entry: %v", r.m.history)
	}
	if dirEntries(t, r.dir) != 1 {
		t.Fatal("no downloaded file")
	}
	r.pump(t, func() bool { return r.d.saw("load") && r.d.saw("play") && r.m.player.frames > 0 })
	r.pump(t, func() bool { return strings.Contains(statusLines(r.m), "0:12 / 0:32") })
	if !strings.Contains(statusLines(r.m), "no audio in this profile") {
		t.Fatalf("%q", statusLines(r.m))
	}
}

func TestVideoOffRefuses(t *testing.T) {
	srv := videoServer(t)
	m := contentModel(t, openProfile(profile.MediaAnimation), srv.URL)
	run(t, m, srv.URL+"/v.mp4")
	if m.player != nil || m.status.text != "video is off in this profile" {
		t.Fatalf("status %q", m.status.text)
	}
}

func TestPlayerKeys(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	run(t, r.m, srv.URL+"/v.mp4")
	r.pump(t, func() bool { return r.m.player.status.State == ntmediabox.Playing })
	r.m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	r.pump(t, func() bool { return r.d.saw("pause") })
	r.m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	r.pump(t, func() bool { return r.d.saw("seek 17s") })
	r.m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	r.pump(t, func() bool { return r.d.saw("seek 7s") })
}

func TestEscReturnsAndRemovesFile(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	r.m.current = mustURL(t, srv.URL+"/")
	run(t, r.m, srv.URL+"/v.mp4")
	r.m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if r.m.player != nil || dirEntries(t, r.dir) != 0 {
		t.Fatalf("player %v files %d", r.m.player, dirEntries(t, r.dir))
	}
}

func TestEscQuitsWithNoPageBeneath(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	run(t, r.m, srv.URL+"/v.mp4")
	r.m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !r.m.closing {
		t.Fatal("Esc with no page beneath must quit")
	}
}

func TestQuitRemovesTempDir(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	run(t, r.m, srv.URL+"/v.mp4")
	r.m.Cleanup()
	if _, err := os.Stat(r.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp dir: %v", err)
	}
}

func TestVideoOverCapRemovesFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.(http.Flusher).Flush() // no Content-Length
		w.Write(make([]byte, 4096))
	}))
	defer srv.Close()
	r := videoModel(t, srv.URL)
	r.m.profile.MaxMediaBytes = 100
	run(t, r.m, srv.URL+"/v.mp4")
	if r.m.player != nil || !strings.Contains(r.m.status.text, "over the media limit") || dirEntries(t, r.dir) != 0 {
		t.Fatalf("player %v status %q files %d", r.m.player, r.m.status.text, dirEntries(t, r.dir))
	}
}

func TestDownloadCancelled(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(make([]byte, 64))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	r := videoModel(t, srv.URL)
	cmd := r.m.navigate(srv.URL+"/v.mp4", pushNew, reachAny)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	deadline := time.Now().Add(patience)
	for dirEntries(t, r.dir) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("download never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	select {
	case msg := <-done:
		r.m.Update(msg)
	case <-time.After(patience):
		t.Fatal("download not cancelled")
	}
	if r.m.player != nil || dirEntries(t, r.dir) != 0 {
		t.Fatalf("player %v files %d", r.m.player, dirEntries(t, r.dir))
	}
}

func TestSecondActivationIgnored(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(make([]byte, 64))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	r := videoModel(t, srv.URL)
	r.m.current = mustURL(t, srv.URL+"/")
	cmd := r.m.navigate(srv.URL+"/v.mp4", pushNew, reachAny)
	go cmd()
	deadline := time.Now().Add(patience)
	for dirEntries(t, r.dir) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("download never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if again := r.m.admit(loop.NavigateMsg{Href: srv.URL + "/v.mp4"}); again != nil {
		t.Fatal("a second download started")
	}
	r.m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
}

func TestHelperDeathDuringPlay(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	r.m.current = mustURL(t, srv.URL+"/")
	run(t, r.m, srv.URL+"/v.mp4")
	r.end.Close()
	r.pump(t, func() bool { return strings.Contains(statusLines(r.m), "media helper stopped; restart ntbrowser") })
	r.m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if r.m.player != nil || dirEntries(t, r.dir) != 0 {
		t.Fatalf("player %v files %d", r.m.player, dirEntries(t, r.dir))
	}
	run(t, r.m, srv.URL+"/v.mp4")
	if r.m.player != nil || !strings.Contains(r.m.status.text, "media helper stopped") {
		t.Fatalf("after death: %q", r.m.status.text)
	}
}

func TestNoHelperMeansLine(t *testing.T) {
	srv := videoServer(t)
	p := openProfile(profile.MediaVideo)
	m := contentModel(t, p, srv.URL)
	run(t, m, srv.URL+"/v.mp4")
	if m.player != nil || !strings.Contains(m.status.text, "can't display") {
		t.Fatalf("%q", m.status.text)
	}
}

// Before Kitty is detected a picture reports about one pixel per cell; video
// must still be requested at a usable size.
func TestFramePixelsIgnoresGlyphGeometry(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	run(t, r.m, srv.URL+"/v.mp4")
	if px := r.m.framePixels(); px.X < r.m.cols*10 || px.Y < r.m.viewRows()*10 {
		t.Fatalf("frame size %v for %d×%d cells", px, r.m.cols, r.m.viewRows())
	}
}

// Kitty discovery happened once, for the viewer; a player or image view must
// share it, or it draws in glyphs.
func TestPicturesShareTheViewerSession(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	run(t, r.m, srv.URL+"/v.mp4")
	if r.m.player.pic.Session() != r.m.viewer.Picture().Session() {
		t.Fatal("the player has its own terminal session")
	}
}

func TestImageViewSharesTheViewerSession(t *testing.T) {
	m := newModel(t, NavFree)
	view, _ := newMediaView(image.NewRGBA(image.Rect(0, 0, 2, 2)), nil, m.pictureOptions(), 80, 20)
	if view.pic.Session() != m.viewer.Picture().Session() {
		t.Fatal("the image view has its own terminal session")
	}
}

func TestQuitDuringPlayRemovesFile(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	run(t, r.m, srv.URL+"/v.mp4")
	r.m.quit()
	if dirEntries(t, r.dir) != 0 {
		t.Fatal("the video stayed on disk after quit")
	}
}

func TestSecondPlayerClosesFirst(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	run(t, r.m, srv.URL+"/v.mp4")
	first := r.m.player
	run(t, r.m, srv.URL+"/v.mp4")
	if r.m.player == first || dirEntries(t, r.dir) != 1 {
		t.Fatalf("files %d", dirEntries(t, r.dir))
	}
}

func TestOverlayBlocksPageInput(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	r.m.current = mustURL(t, srv.URL+"/")
	run(t, r.m, srv.URL+"/v.mp4")
	r.m.Update(tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	if r.m.pressed {
		t.Fatal("a click reached the page beneath the player")
	}
	m := newModel(t, NavFree)
	m.media, _ = newMediaView(image.NewRGBA(image.Rect(0, 0, 2, 2)), nil, m.pictureOptions(), 80, 20)
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab}); cmd != nil {
		t.Fatal("a key reached the page beneath the picture")
	}
	m.Update(tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	if m.pressed {
		t.Fatal("a click reached the page beneath the picture")
	}
}

func TestLinkDuringDownloadNavigates(t *testing.T) {
	m := newModel(t, NavFree)
	m.downloading.Store(true)
	m.navURL = "https://page.test/v.mp4"
	if cmd := m.admit(loop.NavigateMsg{Href: "https://page.test/v.mp4"}); cmd != nil {
		t.Fatal("the same video started twice")
	}
	if cmd := m.admit(loop.NavigateMsg{Href: "https://page.test/other"}); cmd == nil {
		t.Fatal("a link to another page was ignored")
	}
}

func TestBridgeKillsHelperWhenTheConnectionEnds(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			if _, err := b.Read(buf); err != nil {
				return
			}
		}
	}()
	killed := make(chan struct{})
	if _, err := NewMediaBridge(a, func(tea.Msg) {}, func() { close(killed) }); err != nil {
		t.Fatal(err)
	}
	b.Write([]byte{0, 0, 0, 1, 99}) // a protocol violation
	select {
	case <-killed:
	case <-time.After(patience):
		t.Fatal("helper not killed")
	}
}

func TestStalledDownloadTimesOut(t *testing.T) {
	old := navIdle
	navIdle = 100 * time.Millisecond
	defer func() { navIdle = old }()
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(make([]byte, 64))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	r := videoModel(t, srv.URL)
	msg := r.m.navigate(srv.URL+"/v.mp4", pushNew, reachAny)().(pageLoadedMsg)
	if msg.video != "" || dirEntries(t, r.dir) != 0 {
		t.Fatalf("%+v files %d", msg, dirEntries(t, r.dir))
	}
}

// Messages for the viewer's picture must reach it while an overlay is up, or
// its render job never finishes and the page stays frozen afterwards.
func TestPageRecoversAfterOverlayAndResize(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	r.m.current = mustURL(t, srv.URL+"/")
	m := r.m
	drain(t, m, m.viewer.Picture().SetTerminal(false, 10, 20))
	run(t, m, srv.URL+"/v.mp4")
	drain(t, m, m.onResizeForTest(100, 30))
	drain(t, m, m.closePlayer())
	drain(t, m, frame(t, m, 7, `<p>one</p>`))
	before := m.viewer.View()
	drain(t, m, frame(t, m, 8, `<div style="background:#f00;height:400px">two</div>`))
	if m.viewer.View() == before {
		t.Fatal("the page stayed frozen after the overlay")
	}
}

func (m *Model) onResizeForTest(w, h int) tea.Cmd {
	_, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return cmd
}

func frame(t *testing.T, m *Model, gen uint64, body string) tea.Cmd {
	t.Helper()
	_, cmd := m.Update(cssFrame(t, m, loop.FrameMsg{Gen: gen, Result: renderTall(t, body)}))
	return cmd
}

// drain runs cmd and everything it leads to, feeding results to the model, as
// Bubble Tea would, until nothing is pending.
func drain(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	deadline := time.Now().Add(patience)
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 && time.Now().Before(deadline) {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case msg := <-done:
			switch v := msg.(type) {
			case nil:
			case tea.BatchMsg:
				queue = append(queue, v...)
			default:
				_, next := m.Update(msg)
				queue = append(queue, next)
			}
		case <-time.After(200 * time.Millisecond): // a timer: not part of this drain
		}
	}
}

// A callback may already be queued when Open, Seek, or Stop advances the
// generation. Check again on the UI thread before touching the current player.
func TestPlayerDiscardsStaleMediaMessages(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	go io.Copy(io.Discard, b)
	bridge, err := NewMediaBridge(a, func(tea.Msg) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(t, NavFree)
	m.SetMedia(bridge, t.TempDir())
	m.startPlayer("first")
	for _, advance := range []struct {
		name string
		fn   func()
	}{
		{"seek", func() { bridge.client.Seek(time.Second) }},
		{"replace", func() { m.startPlayer("second") }},
	} {
		t.Run(advance.name, func(t *testing.T) {
			old := bridge.client.Gen()
			advance.fn()
			gen := bridge.client.Gen()
			if gen == old {
				t.Fatal("generation did not advance")
			}
			m.onVideo(videoStatusMsg{ntmediabox.Status{Gen: gen, State: ntmediabox.Playing}})
			m.onVideo(videoStatusMsg{ntmediabox.Status{Gen: old, State: ntmediabox.Failed, Err: "old failure"}})
			if m.player.status.Gen != gen {
				t.Fatal("stale status replaced current status")
			}
			before := m.player.frames
			bridge.latest.Store(&ntmediabox.Frame{Gen: old, Image: image.NewRGBA(image.Rect(0, 0, 2, 2))})
			m.onVideo(videoFrameMsg{})
			if m.player.frames != before {
				t.Fatal("stale frame was displayed")
			}
			bridge.latest.Store(&ntmediabox.Frame{Gen: gen, Image: image.NewRGBA(image.Rect(0, 0, 2, 2))})
			m.onVideo(videoFrameMsg{})
			if m.player.frames != before+1 {
				t.Fatal("current frame was dropped")
			}
		})
	}
	m.Cleanup()
}

func TestPlayerPassesProfileMediaLimit(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	r.m.profile.MaxMediaBytes = 12345
	run(t, r.m, srv.URL+"/v.mp4")
	r.pump(t, func() bool { return r.d.saw("load") })
	r.d.mu.Lock()
	req := r.d.request
	r.d.mu.Unlock()
	if req.Limits.MaxFileBytes != 12345 || req.Name != r.m.player.name || req.Autoplay || req.Loop || len(req.Captions) != 0 {
		t.Fatalf("unexpected worker request: %+v", req)
	}
}

func TestBridgeClosesHelperExactlyOnce(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go io.Copy(io.Discard, b)
	var kills atomic.Int32
	bridge, err := NewMediaBridge(a, func(tea.Msg) {}, func() { kills.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(t, NavFree)
	m.SetMedia(bridge, "")
	m.Cleanup()
	m.Cleanup()
	if got := kills.Load(); got != 1 {
		t.Fatalf("helper killed %d times", got)
	}
}

func TestBridgeKillsHelperOnHandshakeFailure(t *testing.T) {
	a, b := net.Pipe()
	b.Close()
	var kills atomic.Int32
	if _, err := NewMediaBridge(a, func(tea.Msg) {}, func() { kills.Add(1) }); err == nil {
		t.Fatal("handshake succeeded")
	}
	if got := kills.Load(); got != 1 {
		t.Fatalf("helper killed %d times", got)
	}
}

func TestBlockedUIDoesNotBlockHelperTermination(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	blocked := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	var once sync.Once
	killed := make(chan struct{})
	go ntmediabox.Serve(b, func(string) (ntmediabox.Decoder, error) { return &stubDecoder{}, nil })
	bridge, err := NewMediaBridge(a, func(tea.Msg) {
		once.Do(func() { close(blocked) })
		<-release
	}, func() { close(killed) })
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.client.Close()
	select {
	case <-blocked:
	case <-time.After(patience):
		t.Fatal("no UI delivery")
	}
	b.Close()
	select {
	case <-killed:
	case <-time.After(patience):
		t.Fatal("UI blocked protocol termination")
	}
}
