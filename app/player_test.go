package app

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/mediaproc"
	"nimbleterminal.dev/ntbrowser/profile"
)

type stubDecoder struct {
	mu    sync.Mutex
	ops   []string
	state mediaproc.State
}

func (d *stubDecoder) add(s string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ops = append(d.ops, s)
	return nil
}
func (d *stubDecoder) Load(n string) error {
	d.mu.Lock()
	d.state = mediaproc.Paused
	d.mu.Unlock()
	return d.add("load")
}
func (d *stubDecoder) Play() error {
	d.mu.Lock()
	d.state = mediaproc.Playing
	d.mu.Unlock()
	return d.add("play")
}
func (d *stubDecoder) Pause() error               { return d.add("pause") }
func (d *stubDecoder) Seek(t time.Duration) error { return d.add("seek " + t.String()) }
func (d *stubDecoder) Status() (mediaproc.State, time.Duration, time.Duration, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state, 12 * time.Second, 32 * time.Second, nil
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
	go mediaproc.Serve(b, func(string) (mediaproc.Decoder, error) { return d, nil })
	dir := t.TempDir()
	msgs := make(chan tea.Msg, 256)
	bridge, err := NewMediaBridge(a, func(msg tea.Msg) {
		select {
		case msgs <- msg:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	m.SetMedia(bridge, dir)
	return &videoRig{m: m, d: d, msgs: msgs, dir: dir, end: b}
}

func (r *videoRig) pump(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
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
		t.Fatalf("no player; status %q", r.m.status)
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
	if m.player != nil || m.status != "video is off in this profile" {
		t.Fatalf("status %q", m.status)
	}
}

func TestPlayerKeys(t *testing.T) {
	srv := videoServer(t)
	r := videoModel(t, srv.URL)
	run(t, r.m, srv.URL+"/v.mp4")
	r.pump(t, func() bool { return r.m.player.status.State == mediaproc.Playing })
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
	if r.m.player != nil || !strings.Contains(r.m.status, "over the media limit") || dirEntries(t, r.dir) != 0 {
		t.Fatalf("player %v status %q files %d", r.m.player, r.m.status, dirEntries(t, r.dir))
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
	cmd := r.m.navigate(srv.URL+"/v.mp4", pushNew, true)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	deadline := time.Now().Add(2 * time.Second)
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
	case <-time.After(3 * time.Second):
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
	cmd := r.m.navigate(srv.URL+"/v.mp4", pushNew, true)
	go cmd()
	deadline := time.Now().Add(2 * time.Second)
	for dirEntries(t, r.dir) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("download never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if again := r.m.admit(srv.URL+"/v.mp4", false); again != nil {
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
	if r.m.player != nil || !strings.Contains(r.m.status, "media helper stopped") {
		t.Fatalf("after death: %q", r.m.status)
	}
}

func TestNoHelperMeansLine(t *testing.T) {
	srv := videoServer(t)
	p := openProfile(profile.MediaVideo)
	m := contentModel(t, p, srv.URL)
	run(t, m, srv.URL+"/v.mp4")
	if m.player != nil || !strings.Contains(m.status, "can't display") {
		t.Fatalf("%q", m.status)
	}
}
