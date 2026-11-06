// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package subres

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/netpolicy"
)

// fakeTransport answers by absolute URL and records hits and kinds.
type fakeTransport struct {
	mu    sync.Mutex
	hits  map[string]int
	kinds map[string]netpolicy.Kind
	fn    func(ctx context.Context, r netpolicy.Request) (netpolicy.Response, error)
}

func newFake(fn func(ctx context.Context, r netpolicy.Request) (netpolicy.Response, error)) *fakeTransport {
	return &fakeTransport{hits: map[string]int{}, kinds: map[string]netpolicy.Kind{}, fn: fn}
}

func (f *fakeTransport) Do(ctx context.Context, r netpolicy.Request) (netpolicy.Response, error) {
	f.mu.Lock()
	f.hits[r.URL]++
	f.kinds[r.URL] = r.Kind
	f.mu.Unlock()
	return f.fn(ctx, r)
}

func (f *fakeTransport) hit(u string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[u]
}

func (f *fakeTransport) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, v := range f.hits {
		n += v
	}
	return n
}

func css(body string) (netpolicy.Response, error) {
	return netpolicy.Response{Status: 200, Headers: map[string]string{"content-type": "text/css"}, Body: body}, nil
}

func img(ct string, body []byte) (netpolicy.Response, error) {
	return netpolicy.Response{Status: 200, Headers: map[string]string{"content-type": ct}, Body: string(body)}, nil
}

// runner collects posted funcs and runs them on the test goroutine.
type runner struct{ q chan func() }

func newRunner() *runner { return &runner{q: make(chan func(), 1024)} }

func (r *runner) post(f func()) { r.q <- f }

// settle runs posted funcs until cond holds or two seconds pass.
func (r *runner) settle(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !cond() {
		select {
		case f := <-r.q:
			f()
		case <-deadline:
			t.Fatal("condition not met within 2s")
		}
	}
}

// quiet reports whether nothing is posted within d.
func (r *runner) quiet(d time.Duration) bool {
	select {
	case f := <-r.q:
		f()
		return false
	case <-time.After(d):
		return true
	}
}

func testCfg() Config {
	return Config{Timeout: time.Second}.WithDefaults()
}

func newTestSession(t *testing.T, cfg Config, ft *fakeTransport) (*Session, *runner) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := newRunner()
	s := NewSession(ctx, cfg, ft, "https://page.test/", r.post)
	t.Cleanup(s.Close)
	return s, r
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.Set(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func gifBytes(t *testing.T) []byte {
	t.Helper()
	im := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var b bytes.Buffer
	if err := gif.Encode(&b, im, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{"any": ModeAny, "ORIGIN": ModeOrigin, "off": ModeOff}
	for in, want := range cases {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Fatalf("ParseMode(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseMode("x"); err == nil {
		t.Fatal("ParseMode(x) must fail")
	}
}

func TestDefaults(t *testing.T) {
	got := Config{}.WithDefaults()
	want := Config{MaxStylesheets: 16, MaxImages: 64, MaxSheetBytes: 256 << 10, MaxImagePixels: 16 << 20, MaxInFlight: 6, StyleTimeout: 1500 * time.Millisecond, Timeout: 10 * time.Second}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
	if kept := (Config{MaxInFlight: 12}).WithDefaults(); kept.MaxInFlight != 12 {
		t.Fatalf("set field not kept: %+v", kept)
	}
}

func TestStylesheetLoadsOnce(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) { return css("h1{color:red}") })
	s, r := newTestSession(t, testCfg(), ft)
	s.Discover([]string{"a.css"}, nil, nil)
	s.Discover([]string{"a.css"}, nil, nil)
	r.settle(t, func() bool { return !s.StylesPending() })
	if ft.hit("https://page.test/a.css") != 1 || ft.kinds["https://page.test/a.css"] != netpolicy.KindStyle {
		t.Fatalf("hits %v kinds %v", ft.hits, ft.kinds)
	}
	got, rules, ok := s.Style("a.css")
	if !ok || got != "h1{color:red}" || rules != 1 {
		t.Fatalf("Style = %q %d %v", got, rules, ok)
	}
}

func TestBlockingSetClosesAtFirstFrame(t *testing.T) {
	release := make(chan struct{})
	ft := newFake(func(ctx context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		if strings.HasSuffix(r.URL, "b.css") {
			<-release
		}
		return css("p{}")
	})
	s, r := newTestSession(t, testCfg(), ft)
	s.Discover([]string{"a.css"}, nil, nil)
	if !s.StylesPending() {
		t.Fatal("a.css must block before the first frame")
	}
	r.settle(t, func() bool { return !s.StylesPending() })
	s.FirstFrameRendered()
	s.Discover([]string{"b.css"}, nil, nil)
	if s.StylesPending() {
		t.Fatal("a sheet discovered after the first frame must not block")
	}
	close(release)
	r.settle(t, func() bool { _, _, ok := s.Style("b.css"); return ok })
}

func TestModeOffRequestsNothing(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) { return css("p{}") })
	cfg := testCfg()
	cfg.Mode = ModeOff
	s, r := newTestSession(t, cfg, ft)
	s.Discover([]string{"a.css"}, []string{"p.png"}, []string{"https://cdn.test/b.png"})
	if !r.quiet(100 * time.Millisecond) {
		t.Fatal("ModeOff posted work")
	}
	if ft.total() != 0 || s.StylesPending() || len(s.Warnings()) != 0 {
		t.Fatalf("ModeOff made requests: %v", ft.hits)
	}
	if _, _, ok := s.Style("a.css"); ok {
		t.Fatal("ModeOff loaded a sheet")
	}
}

func TestImportsSpliced(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		switch {
		case strings.HasSuffix(r.URL, "/a.css"):
			return css(`@import url(b.css);@import "c.css" (max-width: 400px);p{}`)
		case strings.HasSuffix(r.URL, "/b.css"):
			return css("b{color:blue}")
		case strings.HasSuffix(r.URL, "/c.css"):
			return css("c{color:cyan}")
		}
		return netpolicy.Response{}, errors.New("unexpected " + r.URL)
	})
	s, r := newTestSession(t, testCfg(), ft)
	s.Discover([]string{"a.css"}, nil, nil)
	r.settle(t, func() bool { return !s.StylesPending() })
	got, rules, ok := s.Style("a.css")
	if !ok {
		t.Fatalf("a.css not loaded: %v", s.Warnings())
	}
	bi, ci, pi := strings.Index(got, "b{color:blue}"), strings.Index(got, "@media (max-width: 400px) {"), strings.Index(got, "p{}")
	if bi < 0 || ci < 0 || pi < 0 || !(bi < ci && ci < pi) || !strings.Contains(got, "c{color:cyan}") {
		t.Fatalf("spliced text wrong:\n%s", got)
	}
	if rules != 3 {
		t.Fatalf("rules = %d, want 3", rules)
	}
	for _, u := range []string{"https://page.test/a.css", "https://page.test/b.css", "https://page.test/c.css"} {
		if ft.hit(u) != 1 {
			t.Fatalf("hits %v", ft.hits)
		}
	}
}

func TestImportResolvesAgainstFinalURL(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		switch r.URL {
		case "https://page.test/a.css":
			return netpolicy.Response{Status: 200, URL: "https://page.test/css/a.css", Headers: map[string]string{"content-type": "text/css"}, Body: "@import url(b.css);"}, nil
		case "https://page.test/css/b.css":
			return css("b{}")
		}
		return netpolicy.Response{}, errors.New("unexpected " + r.URL)
	})
	s, r := newTestSession(t, testCfg(), ft)
	s.Discover([]string{"a.css"}, nil, nil)
	r.settle(t, func() bool { return !s.StylesPending() })
	if ft.hit("https://page.test/css/b.css") != 1 {
		t.Fatalf("import must resolve against the final URL: %v", ft.hits)
	}
	if got, _, ok := s.Style("a.css"); !ok || !strings.Contains(got, "b{}") {
		t.Fatalf("Style = %q %v", got, ok)
	}
}

func TestImportDepthCycleAndUnsupported(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		switch {
		case strings.HasSuffix(r.URL, "/a.css"):
			return css("@import url(b.css);a{}")
		case strings.HasSuffix(r.URL, "/b.css"):
			return css("@import url(c.css);b{}")
		case strings.HasSuffix(r.URL, "/c.css"):
			return css("@import url(d.css);c{}")
		case strings.HasSuffix(r.URL, "/d.css"):
			return css("@import url(e.css);d{}")
		case strings.HasSuffix(r.URL, "/e.css"):
			return css("e{}")
		case strings.HasSuffix(r.URL, "/self.css"):
			return css("@import url(self.css);s{}")
		case strings.HasSuffix(r.URL, "/layer.css"):
			return css("@import url(x.css) layer(l);l{}")
		case strings.HasSuffix(r.URL, "/x.css"):
			return css("x{}")
		}
		return netpolicy.Response{}, errors.New("unexpected " + r.URL)
	})
	s, r := newTestSession(t, testCfg(), ft)
	s.Discover([]string{"a.css", "self.css", "layer.css"}, nil, nil)
	r.settle(t, func() bool { return !s.StylesPending() })
	if ft.hit("https://page.test/d.css") != 1 || ft.hit("https://page.test/e.css") != 0 {
		t.Fatalf("depth: %v", ft.hits)
	}
	if ft.hit("https://page.test/self.css") != 1 || ft.hit("https://page.test/x.css") != 0 {
		t.Fatalf("cycle/layer: %v", ft.hits)
	}
	w := strings.Join(s.Warnings(), "\n")
	for _, want := range []string{"depth", "cycle", "layer"} {
		if !strings.Contains(w, want) {
			t.Fatalf("warnings missing %q:\n%s", want, w)
		}
	}
	if got, _, ok := s.Style("a.css"); !ok || !strings.Contains(got, "d{}") || strings.Contains(got, "e{}") {
		t.Fatalf("a.css = %q %v", got, ok)
	}
}

func TestImportsCountTowardSheetCap(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		if strings.HasSuffix(r.URL, "/a.css") {
			return css("@import url(b.css);@import url(c.css);@import url(d.css);a{}")
		}
		return css("x{}")
	})
	cfg := testCfg()
	cfg.MaxStylesheets = 3
	s, r := newTestSession(t, cfg, ft)
	s.Discover([]string{"a.css"}, nil, nil)
	r.settle(t, func() bool { return !s.StylesPending() })
	if ft.hit("https://page.test/d.css") != 0 || ft.hit("https://page.test/c.css") != 1 {
		t.Fatalf("cap: %v", ft.hits)
	}
	if w := s.Warnings(); len(w) != 1 {
		t.Fatalf("warnings = %v", w)
	}
}

func TestStyleReportsRuleCount(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		return css("a,b,c{} @media print{d,e,f,g{} h{}} i,j,k{}")
	})
	s, r := newTestSession(t, testCfg(), ft)
	s.Discover([]string{"a.css"}, nil, nil)
	r.settle(t, func() bool { return !s.StylesPending() })
	if _, rules, ok := s.Style("a.css"); !ok || rules != 11 {
		t.Fatalf("rules = %d %v", rules, ok)
	}
}

func TestSheetTooLargeAndWrongType(t *testing.T) {
	cfg := testCfg()
	cfg.MaxSheetBytes = 100
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		if strings.HasSuffix(r.URL, "/big.css") {
			return css(strings.Repeat("a", 101))
		}
		return netpolicy.Response{}, netpolicy.ErrDenied
	})
	s, r := newTestSession(t, cfg, ft)
	s.Discover([]string{"big.css", "denied.css"}, nil, nil)
	r.settle(t, func() bool { return !s.StylesPending() })
	for _, href := range []string{"big.css", "denied.css"} {
		if _, _, ok := s.Style(href); ok {
			t.Fatalf("%s must not load", href)
		}
	}
	w := strings.Join(s.Warnings(), "\n")
	if !strings.Contains(w, "big.css") || !strings.Contains(w, "denied.css") {
		t.Fatalf("warnings:\n%s", w)
	}
}

func TestImagesDecoded(t *testing.T) {
	webp, err := os.ReadFile("testdata/tiny.webp")
	if err != nil {
		t.Fatal(err)
	}
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		switch {
		case strings.HasSuffix(r.URL, "/p.png"):
			return img("image/png", pngBytes(t, 2, 2))
		case strings.HasSuffix(r.URL, "/g.gif"):
			return img("image/gif", gifBytes(t))
		case strings.HasSuffix(r.URL, "/t.webp"):
			return img("image/webp", webp)
		case strings.HasSuffix(r.URL, "/l.svg"):
			return img("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18"><rect width="18" height="18" fill="green"/></svg>`))
		}
		return netpolicy.Response{}, errors.New("unexpected " + r.URL)
	})
	s, r := newTestSession(t, testCfg(), ft)
	s.Discover(nil, []string{"p.png", "g.gif", "t.webp", "l.svg"}, nil)
	r.settle(t, func() bool { return len(s.Images()) == 4 })
	want := map[string]image.Point{"p.png": {2, 2}, "g.gif": {2, 2}, "t.webp": {2, 2}, "l.svg": {18, 18}}
	for k, sz := range want {
		im := s.Images()[k]
		if im == nil || im.Bounds().Size() != sz {
			t.Fatalf("%s: %v (warnings %v)", k, im, s.Warnings())
		}
	}
	if ft.kinds["https://page.test/p.png"] != netpolicy.KindImage {
		t.Fatalf("kinds %v", ft.kinds)
	}
}

func TestSVGDefaultAndClampSize(t *testing.T) {
	if w, h := svgSize(`<svg viewBox="0 0 10 5"></svg>`); w != 10 || h != 5 {
		t.Fatalf("viewBox: %d×%d", w, h)
	}
	if w, h := svgSize(`<svg></svg>`); w != 64 || h != 64 {
		t.Fatalf("default: %d×%d", w, h)
	}
	if w, h := svgSize(`<svg width="5000" height="12px"></svg>`); w != 1024 || h != 12 {
		t.Fatalf("clamp: %d×%d", w, h)
	}
}

func TestDataURLImageNeedsNoTransport(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		return netpolicy.Response{}, errors.New("no transport expected")
	})
	s, r := newTestSession(t, testCfg(), ft)
	key := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes(t, 2, 2))
	s.Discover(nil, []string{key}, []string{key})
	r.settle(t, func() bool { return len(s.Images()) == 1 })
	if ft.total() != 0 {
		t.Fatalf("transport used: %v", ft.hits)
	}
	if s.Images()[key] == nil || s.Images()[key].Bounds().Size() != image.Pt(2, 2) {
		t.Fatal("data: image not decoded")
	}
	if len(s.BackgroundImages()) != 0 || len(s.Warnings()) != 0 {
		t.Fatalf("data: background must be skipped silently: %v %v", s.BackgroundImages(), s.Warnings())
	}
}

func TestImageCapsAndPixelBudget(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		if strings.HasSuffix(r.URL, "/big.png") {
			return img("image/png", pngBytes(t, 3, 3))
		}
		return img("image/png", pngBytes(t, 2, 2))
	})
	cfg := testCfg()
	cfg.MaxImages = 2
	cfg.MaxImagePixels = 8
	cfg.MaxInFlight = 1
	s, r := newTestSession(t, cfg, ft)
	s.Discover(nil, []string{"a.png"}, nil)
	r.settle(t, func() bool { return len(s.Images()) == 1 })
	s.Discover(nil, []string{"big.png", "third.png"}, nil)
	r.settle(t, func() bool { return ft.hit("https://page.test/big.png") == 1 && r.quiet(50*time.Millisecond) })
	if ft.hit("https://page.test/third.png") != 0 {
		t.Fatalf("third image must be ignored past MaxImages: %v", ft.hits)
	}
	if s.Images()["big.png"] != nil {
		t.Fatal("3×3 must be dropped over the pixel budget")
	}
	w := strings.Join(s.Warnings(), "\n")
	if !strings.Contains(w, "pixel budget") || !strings.Contains(w, "images") {
		t.Fatalf("warnings:\n%s", w)
	}
}

func TestBackgroundsKeyedAsGiven(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		return img("image/png", pngBytes(t, 2, 2))
	})
	s, r := newTestSession(t, testCfg(), ft)
	key := "https://cdn.test/css/arrow.png"
	s.Discover(nil, nil, []string{key})
	r.settle(t, func() bool { return len(s.BackgroundImages()) == 1 })
	if s.BackgroundImages()[key] == nil || len(s.Images()) != 0 || ft.hit(key) != 1 {
		t.Fatalf("backgrounds: %v images: %v hits %v", s.BackgroundImages(), s.Images(), ft.hits)
	}
	m := s.BackgroundImages()
	delete(m, key)
	if s.BackgroundImages()[key] == nil {
		t.Fatal("BackgroundImages must return a copy")
	}
}

func TestBusyIsRetriedOnce(t *testing.T) {
	var calls atomic.Int32
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		if calls.Add(1) == 1 {
			return netpolicy.Response{}, netpolicy.ErrBusy
		}
		return css("p{}")
	})
	s, r := newTestSession(t, testCfg(), ft)
	s.Discover([]string{"a.css"}, nil, nil)
	r.settle(t, func() bool { return !s.StylesPending() })
	if _, _, ok := s.Style("a.css"); !ok || ft.hit("https://page.test/a.css") != 2 {
		t.Fatalf("retry: ok=%v hits=%v", ok, ft.hits)
	}
}

func TestMaxInFlightQueue(t *testing.T) {
	var inflight, high atomic.Int32
	release := make(chan struct{})
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		n := inflight.Add(1)
		for {
			h := high.Load()
			if n <= h || high.CompareAndSwap(h, n) {
				break
			}
		}
		<-release
		inflight.Add(-1)
		return img("image/png", pngBytes(t, 2, 2))
	})
	cfg := testCfg()
	cfg.MaxInFlight = 2
	s, r := newTestSession(t, cfg, ft)
	s.Discover(nil, []string{"1.png", "2.png", "3.png", "4.png", "5.png"}, nil)
	time.Sleep(50 * time.Millisecond)
	close(release)
	r.settle(t, func() bool { return len(s.Images()) == 5 })
	if high.Load() > 2 {
		t.Fatalf("high-water mark %d > MaxInFlight 2", high.Load())
	}
}

func TestWarningsDrain(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		return netpolicy.Response{}, netpolicy.ErrDenied
	})
	s, r := newTestSession(t, testCfg(), ft)
	s.Discover([]string{"a.css"}, nil, nil)
	r.settle(t, func() bool { return !s.StylesPending() })
	if w := s.Warnings(); len(w) != 1 {
		t.Fatalf("warnings = %v", w)
	}
	if w := s.Warnings(); w != nil {
		t.Fatalf("second drain = %v", w)
	}
}

func TestDiscoverAfterCloseIsInert(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) { return css("p{}") })
	s, r := newTestSession(t, testCfg(), ft)
	s.Close()
	s.Discover([]string{"a.css"}, []string{"p.png"}, nil)
	if !r.quiet(100*time.Millisecond) || ft.total() != 0 {
		t.Fatalf("closed session did work: %v", ft.hits)
	}
}

func TestCloseCancelsInFlight(t *testing.T) {
	cancelled := make(chan struct{})
	ft := newFake(func(ctx context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		<-ctx.Done()
		close(cancelled)
		return netpolicy.Response{}, ctx.Err()
	})
	s, _ := newTestSession(t, testCfg(), ft)
	s.Discover([]string{"a.css"}, nil, nil)
	s.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the in-flight request")
	}
}

func TestSheetsRequestedBeforeQueuedImages(t *testing.T) {
	var mu sync.Mutex
	var order []string
	release := make(chan struct{})
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		mu.Lock()
		order = append(order, r.URL)
		mu.Unlock()
		if strings.HasSuffix(r.URL, ".png") {
			<-release
			return img("image/png", pngBytes(t, 2, 2))
		}
		return css("p{}")
	})
	cfg := testCfg()
	cfg.MaxInFlight = 1
	s, r := newTestSession(t, cfg, ft)
	s.Discover(nil, []string{"1.png", "2.png", "3.png"}, nil)
	s.Discover([]string{"late.css"}, nil, nil) // a <link> a script added: must not wait behind the images
	close(release)
	r.settle(t, func() bool { return !s.StylesPending() })
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 || !strings.HasSuffix(order[1], "late.css") {
		t.Fatalf("stylesheet queued behind images: %v", order)
	}
}

func TestPercentEncodedSVGDataURL(t *testing.T) {
	ft := newFake(func(_ context.Context, r netpolicy.Request) (netpolicy.Response, error) {
		return netpolicy.Response{}, errors.New("no transport expected")
	})
	s, r := newTestSession(t, testCfg(), ft)
	key := "data:image/svg+xml," + url.PathEscape(`<svg xmlns="http://www.w3.org/2000/svg" width="6" height="6"><rect width="6" height="6" fill="green"/></svg>`)
	s.Discover(nil, []string{key}, nil)
	r.settle(t, func() bool { return len(s.Images()) == 1 || len(s.Warnings()) > 0 })
	im := s.Images()[key]
	if im == nil || im.Bounds().Size() != image.Pt(6, 6) {
		t.Fatalf("percent-encoded SVG data URL not decoded: %v", im)
	}
}

func TestImageStatusDoesNotRetainOverBudgetKeys(t *testing.T) {
	cfg := testCfg()
	cfg.MaxImages = 1
	ft := newFake(func(context.Context, netpolicy.Request) (netpolicy.Response, error) {
		return netpolicy.Response{}, errors.New("failed")
	})
	s, r := newTestSession(t, cfg, ft)
	s.ImageStatus("/first")
	r.settle(t, func() bool { _, _, done := s.ImageStatus("/first"); return done })
	for i := 0; i < 1000; i++ {
		w, h, done := s.ImageStatus(fmt.Sprintf("/over/%d", i))
		if !done || w != 0 || h != 0 {
			t.Fatal("over-cap image accepted")
		}
	}
	if len(s.images) != 1 || ft.total() != 1 {
		t.Fatalf("unbounded rejected requests: entries=%d requests=%d", len(s.images), ft.total())
	}
}
