package loop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/netpolicy"
)

// The pages under examples/ are the acceptance corpus: each logs a sentinel
// when it has done its job, so "done" is observable without reading pixels.
func loadExample(t *testing.T, name string, srv *httptest.Server) (*Loop, chanSink) {
	t.Helper()
	html, err := os.ReadFile(filepath.Join("..", "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chanSink, 4096)
	l := New(ctx, Config{Width: 400, MinFrame: time.Millisecond}, sink)
	go l.Run()
	var f *netpolicy.Fetcher
	base := "https://example.test/"
	if srv != nil {
		u, _ := url.Parse(srv.URL + "/")
		base = u.String()
		f = netpolicy.New(netpolicy.Policy{Origin: u, AllowPrivate: true})
	}
	l.LoadPage(string(html), base, f)
	return l, sink
}

func waitLog(t *testing.T, sink chanSink, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("never saw log %q", want)
		case m := <-sink:
			if lm, ok := m.(LogMsg); ok {
				if lm.Line == want {
					return
				}
				if lm.Level == "error" {
					t.Fatalf("page error: %s", lm.Line)
				}
			}
		}
	}
}

func TestExampleCounter(t *testing.T) {
	_, s := loadExample(t, "counter.html", nil)
	waitLog(t, s, "counter done 5")
}

func TestExampleRAF(t *testing.T) {
	_, s := loadExample(t, "raf.html", nil)
	waitLog(t, s, "raf done 3")
}

func TestExampleFetchList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["a","b","c"]`))
	}))
	defer srv.Close()
	_, s := loadExample(t, "fetch-list.html", srv)
	waitLog(t, s, "fetch done 3")
}

func TestExampleLinks(t *testing.T) {
	l, s := loadExample(t, "links.html", nil)
	var frame FrameMsg
	deadline := time.After(5 * time.Second)
	for frame.Result == nil {
		select {
		case <-deadline:
			t.Fatal("no frame")
		case m := <-s:
			if f, ok := m.(FrameMsg); ok && f.Err == nil {
				frame = f
			}
		}
	}
	if len(frame.Result.Links) != 2 {
		t.Fatalf("links: %+v", frame.Result.Links)
	}
	for _, link := range frame.Result.Links {
		l.LinkActivated(link)
	}
	waitLog(t, s, "intercepted")
	deadline = time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("second link never navigated")
		case m := <-s:
			if n, ok := m.(NavigateMsg); ok {
				if strings.Contains(n.Href, "first") {
					t.Fatal("intercepted link navigated")
				}
				return
			}
		}
	}
}
