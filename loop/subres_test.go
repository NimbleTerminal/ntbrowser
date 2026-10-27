package loop

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/subres"
)

// hits counts requests per path across a test server.
type hits struct {
	mu sync.Mutex
	m  map[string]int
	hd map[string]string // Accept header seen per path
}

func newHits() *hits { return &hits{m: map[string]int{}, hd: map[string]string{}} }

func (h *hits) count(r *http.Request) {
	h.mu.Lock()
	h.m[r.URL.Path]++
	h.hd[r.URL.Path] = r.Header.Get("Accept")
	h.mu.Unlock()
}

func (h *hits) get(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.m[path]
}

func (h *hits) accept(path string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hd[path]
}

func serve(ct string, body []byte) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ct)
		w.Write(body)
	}
}

// newSubresLoop builds a loop whose subresource transport is a netpolicy
// fetcher for the page base (AllowPrivate for httptest's loopback address).
func newSubresLoop(t *testing.T, cfg subres.Config) (*Loop, chanSink) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chanSink, 512)
	l := New(ctx, Config{Width: 400, MinFrame: time.Millisecond, Subresources: cfg, SubresourceTransport: func(base string) subres.Transport {
		u, _ := url.Parse(base)
		return netpolicy.New(netpolicy.Policy{Origin: u, AllowPrivate: true, AnyOrigin: cfg.Mode == subres.ModeAny, NoDowngradeFrom: u.Scheme, MaxBody: 1 << 20, MaxInFlight: 6})
	}}, sink)
	go l.Run()
	return l, sink
}

func frameWhere(t *testing.T, sink chanSink, timeout time.Duration, cond func(FrameMsg) bool) FrameMsg {
	t.Helper()
	deadline := time.After(timeout)
	var last FrameMsg
	var all []string // loader warnings accumulate across frames: a completion's warning rides the next frame only
	for {
		select {
		case <-deadline:
			t.Fatalf("no matching frame within %v; last: err=%v imgs=%v warnings=%v", timeout, last.Err, imgBoxes(last.Result), all)
		case m := <-sink:
			if f, ok := m.(FrameMsg); ok {
				all = append(all, f.Warnings...)
				f.Warnings = append([]string(nil), all...)
				last = f
				if cond(f) {
					return f
				}
			}
		}
	}
}

func nextFrame(t *testing.T, sink chanSink) FrameMsg {
	t.Helper()
	return frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return f.Result != nil })
}

// boxColor is the colour of the first text run inside the first element
// box with the given tag (nthtml keeps colour on "text" boxes).
func boxColor(res *document.Result, tag string) color.RGBA {
	if res == nil {
		return color.RGBA{}
	}
	for _, e := range res.Boxes {
		if e.Tag != tag {
			continue
		}
		for _, b := range res.Boxes {
			if b.Tag == "text" && b.Text != "" && b.Bounds.In(e.Bounds) {
				return b.Color
			}
		}
		return color.RGBA{}
	}
	return color.RGBA{}
}

func imgBoxes(res *document.Result) []image.Point {
	var out []image.Point
	if res == nil {
		return nil
	}
	for _, b := range res.Boxes {
		if b.Tag == "img" {
			out = append(out, b.Bounds.Size())
		}
	}
	return out
}

func isRed(c color.RGBA) bool   { return c.R > 200 && c.G < 60 && c.B < 60 }
func isGreen(c color.RGBA) bool { return c.G > 200 && c.R < 60 && c.B < 60 }
func isBlue(c color.RGBA) bool  { return c.B > 200 && c.R < 60 && c.G < 60 }

func pngBytes(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func selectors(n int, decl string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, ".c%d", i)
	}
	b.WriteString("{" + decl + "}")
	return b.String()
}

func TestStyledFirstFrame(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		serve("text/css", []byte("h1{color:#ff0000}"))(w, r)
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<!doctype html><html><head><link rel="stylesheet" href="/s.css"></head><body><h1>Hi</h1><p id="p">0</p>
<script>let n=0; const tick=()=>{ n++; document.getElementById("p").textContent=String(n); if(n<5) setTimeout(tick,5); }; setTimeout(tick,5);</script></body></html>`, srv.URL+"/", nil)
	first := nextFrame(t, sink)
	if c := boxColor(first.Result, "h1"); !isRed(c) {
		t.Fatalf("first frame h1 colour %v, want red (warnings %v)", c, first.Warnings)
	}
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool {
		txt, _ := f.Result.TTY(context.Background(), 60)
		return strings.Contains(txt, "5")
	})
	if h.get("/s.css") != 1 {
		t.Fatalf("s.css fetched %d times", h.get("/s.css"))
	}
}

func TestStyleTimeoutUnstyledThenStyled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		serve("text/css", []byte("h1{color:#ff0000}"))(w, r)
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{StyleTimeout: 200 * time.Millisecond})
	start := time.Now()
	l.LoadPage(`<link rel="stylesheet" href="/slow.css"><h1>Hi</h1>`, srv.URL+"/", nil)
	first := nextFrame(t, sink)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("first frame took %v, StyleTimeout is 200ms", d)
	}
	if c := boxColor(first.Result, "h1"); isRed(c) {
		t.Fatal("first frame should be unstyled")
	}
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return isRed(boxColor(f.Result, "h1")) })
}

func TestBlockingSetIncludesScriptAddedLink(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		switch r.URL.Path {
		case "/late.css":
			serve("text/css", []byte("h1{color:#ff0000}"))(w, r)
		case "/later.css":
			serve("text/css", []byte("h2{color:#00ff00}"))(w, r)
		}
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<body><h1>Hi</h1><h2>Sub</h2>
<script>
const add = href => { const l = document.createElement("link"); l.setAttribute("rel","stylesheet"); l.setAttribute("href",href); document.body.appendChild(l); };
add("/late.css");
setTimeout(() => add("/later.css"), 50);
</script></body>`, srv.URL+"/", nil)
	first := nextFrame(t, sink)
	if c := boxColor(first.Result, "h1"); !isRed(c) {
		t.Fatalf("a link added during load must block the first frame; h1=%v warnings=%v", c, first.Warnings)
	}
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return isGreen(boxColor(f.Result, "h2")) })
	if h.get("/late.css") != 1 || h.get("/later.css") != 1 {
		t.Fatalf("hits %v", h.m)
	}
}

func TestLinkMediaAndResize(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		serve("text/css", []byte("h1{color:#00ff00}"))(w, r)
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<link rel="stylesheet" media="(max-width: 300px)" href="/m.css"><h1>Hi</h1>`, srv.URL+"/", nil)
	first := nextFrame(t, sink)
	if isGreen(boxColor(first.Result, "h1")) {
		t.Fatal("media (max-width: 300px) must not apply at 400 px")
	}
	l.Resize(250)
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return isGreen(boxColor(f.Result, "h1")) })
	if h.get("/m.css") != 1 {
		t.Fatalf("m.css fetched %d times", h.get("/m.css"))
	}
}

func TestLinkMediaPrintIgnored(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		serve("text/css", []byte("h1{color:#00ff00}"))(w, r)
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<link rel="stylesheet" media="print" href="/p.css"><h1>Hi</h1><script>setTimeout(()=>{document.querySelector("h1").textContent="Hi again"},20)</script>`, srv.URL+"/", nil)
	first := nextFrame(t, sink)
	if isGreen(boxColor(first.Result, "h1")) {
		t.Fatal("print stylesheet applied on screen")
	}
	later := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool {
		txt, _ := f.Result.TTY(context.Background(), 60)
		return strings.Contains(txt, "again")
	})
	if isGreen(boxColor(later.Result, "h1")) {
		t.Fatal("print stylesheet applied on a later frame")
	}
	if h.get("/p.css") != 1 {
		t.Fatalf("p.css fetched %d times", h.get("/p.css"))
	}
}

func TestMixedContent(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		switch r.URL.Path {
		case "/s.css":
			serve("text/css", []byte("h1{color:#ff0000}"))(w, r)
		case "/p.png":
			serve("image/png", pngBytes(t, 2, 2, color.RGBA{0, 0, 255, 255}))(w, r)
		}
	}))
	defer srv.Close()
	page := `<link rel="stylesheet" href="` + srv.URL + `/s.css"><h1>Hi</h1><img src="` + srv.URL + `/p.png">`
	l, sink := newSubresLoop(t, subres.Config{Mode: subres.ModeAny})
	l.LoadPage(page, "https://example.test/", nil)
	first := nextFrame(t, sink)
	if h.get("/s.css") != 0 || h.get("/p.png") != 0 {
		t.Fatalf("https page loaded http subresources: %v", h.m)
	}
	joined := strings.Join(first.Warnings, "\n")
	if strings.Count(joined, "mixed content") != 2 {
		t.Fatalf("want two mixed-content warnings, got %q", joined)
	}
	l.LoadPage(page, srv.URL+"/", nil)
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return isRed(boxColor(f.Result, "h1")) && len(imgBoxes(f.Result)) == 1 && imgBoxes(f.Result)[0] == image.Pt(2, 2) })
	if h.get("/s.css") != 1 || h.get("/p.png") != 1 {
		t.Fatalf("http page: hits %v", h.m)
	}
}

func TestByteAccountingAcrossFrames(t *testing.T) {
	// The sheet fills the 1 MiB serialized budget to within about a KiB of
	// the tiny page, so a drifting charge would refuse it on a later frame.
	pad := strings.Repeat("x", (1<<20)-1500-len("h1{color:#ff0000}/**/"))
	sheet := "h1{color:#ff0000}/*" + pad + "*/"
	srv := httptest.NewServer(http.HandlerFunc(serve("text/css", []byte(sheet))))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{MaxSheetBytes: 2 << 20})
	l.LoadPage(`<link rel="stylesheet" href="/s.css"><h1>Hi</h1><p id="p">0</p>
<script>let n=0; const tick=()=>{ n++; document.getElementById("p").textContent=String(n); if(n<5) setTimeout(tick,5); }; setTimeout(tick,5);</script>`, srv.URL+"/", nil)
	first := nextFrame(t, sink)
	if c := boxColor(first.Result, "h1"); !isRed(c) {
		t.Fatalf("first frame unstyled: %v warnings %v", c, first.Warnings)
	}
	sixth := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool {
		txt, _ := f.Result.TTY(context.Background(), 60)
		return strings.Contains(txt, "5")
	})
	if c := boxColor(sixth.Result, "h1"); !isRed(c) {
		t.Fatalf("frame six lost the style: %v warnings %v", c, sixth.Warnings)
	}
	if strings.Contains(strings.ToLower(strings.Join(sixth.Warnings, "\n")), "byte") {
		t.Fatalf("byte warning: %v", sixth.Warnings)
	}
}

func TestImportMediaAndResize(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		switch r.URL.Path {
		case "/s.css":
			serve("text/css", []byte("@import url(n.css) (max-width: 300px);"))(w, r)
		case "/n.css":
			serve("text/css", []byte("h1{color:#00ff00}"))(w, r)
		}
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<link rel="stylesheet" href="/s.css"><h1>Hi</h1>`, srv.URL+"/", nil)
	first := nextFrame(t, sink)
	if isGreen(boxColor(first.Result, "h1")) {
		t.Fatal("import media must not apply at 400 px")
	}
	l.Resize(250)
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return isGreen(boxColor(f.Result, "h1")) })
	if h.get("/n.css") != 1 || h.get("/s.css") != 1 {
		t.Fatalf("hits %v", h.m)
	}
}

func TestImagesSized(t *testing.T) {
	webp, err := os.ReadFile("../subres/testdata/tiny.webp")
	if err != nil {
		t.Fatal(err)
	}
	gifSpacer := func() []byte {
		// a 1×1 GIF: smallest valid encoding via image/gif would do, but a
		// known literal keeps the fixture independent of encoder output
		return []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff\x21\xf9\x04\x01\x00\x00\x00\x00\x2c\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02\x44\x01\x00\x3b")
	}()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p.png":
			serve("image/png", pngBytes(t, 2, 2, color.RGBA{0, 0, 255, 255}))(w, r)
		case "/s.gif":
			serve("image/gif", gifSpacer)(w, r)
		case "/l.svg":
			serve("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18"><rect width="18" height="18" fill="green"/></svg>`))(w, r)
		case "/t.webp":
			serve("image/webp", webp)(w, r)
		case "/i.ico":
			serve("image/x-icon", []byte("ICO"))(w, r)
		}
	}))
	defer srv.Close()
	data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes(t, 2, 2, color.RGBA{255, 0, 0, 255}))
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<p><img src="/p.png" width="10" height="10"> <img src="/s.gif" width="40" height="1"> <img src="/l.svg" width="18" height="18"> <img src="/t.webp"> <img src="`+data+`"> <img src="/i.ico" alt="icon"></p>`, srv.URL+"/", nil)
	want := []image.Point{{10, 10}, {40, 1}, {18, 18}, {2, 2}, {2, 2}}
	f := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool {
		got := imgBoxes(f.Result)
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return strings.Contains(strings.Join(f.Warnings, "\n"), "i.ico")
	})
	_ = f
}

func TestSVGImageSizedByAttributes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(serve("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><rect width="200" height="100" fill="green"/></svg>`))))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<p><img src="/big.svg" width="12" height="12"></p>`, srv.URL+"/", nil)
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool {
		b := imgBoxes(f.Result)
		return len(b) == 1 && b[0] == image.Pt(12, 12) && len(f.Warnings) == 0
	})
}

func TestModeOriginRefusesCDN(t *testing.T) {
	cdnHits := newHits()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnHits.count(r)
		switch r.URL.Path {
		case "/s.css":
			serve("text/css", []byte("h1{color:#ff0000}"))(w, r)
		case "/p.png":
			serve("image/png", pngBytes(t, 2, 2, color.RGBA{0, 0, 255, 255}))(w, r)
		}
	}))
	defer cdn.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer origin.Close()
	page := `<link rel="stylesheet" href="` + cdn.URL + `/s.css"><h1>Hi</h1><img src="` + cdn.URL + `/p.png">`

	l, sink := newSubresLoop(t, subres.Config{Mode: subres.ModeOrigin})
	l.LoadPage(page, origin.URL+"/", nil)
	first := nextFrame(t, sink)
	if cdnHits.get("/s.css") != 0 || cdnHits.get("/p.png") != 0 {
		t.Fatalf("ModeOrigin reached the CDN: %v", cdnHits.m)
	}
	if len(first.Warnings) == 0 {
		t.Fatal("ModeOrigin refusal must be warned")
	}

	l2, sink2 := newSubresLoop(t, subres.Config{Mode: subres.ModeAny})
	l2.LoadPage(page, origin.URL+"/", nil)
	frameWhere(t, sink2, 5*time.Second, func(f FrameMsg) bool { return isRed(boxColor(f.Result, "h1")) && len(imgBoxes(f.Result)) == 1 })
	if cdnHits.get("/s.css") != 1 || cdnHits.get("/p.png") != 1 {
		t.Fatalf("ModeAny: %v", cdnHits.m)
	}
}

func TestModeOffRequestsNothing(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.count(r) }))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{Mode: subres.ModeOff})
	l.LoadPage(`<link rel="stylesheet" href="/s.css"><h1>Hi</h1><img src="/p.png" alt="x">`, srv.URL+"/", nil)
	nextFrame(t, sink)
	time.Sleep(100 * time.Millisecond)
	if h.get("/s.css") != 0 || h.get("/p.png") != 0 {
		t.Fatalf("ModeOff made requests: %v", h.m)
	}
}

func TestRuleBudgetDropsBigSheet(t *testing.T) {
	big := selectors(4097, "color:#ff0000") + " h1{color:#ff0000}"
	// ~242 KiB and 2,600 selectors, the Wikipedia shape from spec §3
	var wiki strings.Builder
	for i := 0; i < 2600; i++ {
		fmt.Fprintf(&wiki, ".w%d{color:#00ff00}/*%s*/\n", i, strings.Repeat("y", 70))
	}
	wiki.WriteString("h2{color:#00ff00}")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big.css":
			serve("text/css", []byte(big))(w, r)
		case "/wiki.css":
			serve("text/css", []byte(wiki.String()))(w, r)
		}
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<link rel="stylesheet" href="/big.css"><link rel="stylesheet" href="/wiki.css"><h1>Hi</h1><h2>Sub</h2>`, srv.URL+"/", nil)
	f := frameWhere(t, sink, 10*time.Second, func(f FrameMsg) bool { return isGreen(boxColor(f.Result, "h2")) })
	if isRed(boxColor(f.Result, "h1")) {
		t.Fatal("the 4,097-selector sheet must be dropped")
	}
	if !strings.Contains(strings.Join(f.Warnings, "\n"), "rule budget") {
		t.Fatalf("warnings %v", f.Warnings)
	}
}

func TestRuleBudgetIsDocumentOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.css":
			time.Sleep(150 * time.Millisecond) // b.css finishes first
			serve("text/css", []byte(selectors(2999, "margin:0")+" h1{color:#ff0000}"))(w, r)
		case "/b.css":
			serve("text/css", []byte(selectors(1999, "margin:0")+" h2{color:#0000ff}"))(w, r)
		}
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<link rel="stylesheet" href="/a.css"><link rel="stylesheet" href="/b.css"><h1>Hi</h1><h2>Sub</h2>
<script>setTimeout(() => { const s = document.createElement("style"); s.textContent = "`+selectors(1200, "margin:0")+`"; document.body.appendChild(s); }, 600);</script>`, srv.URL+"/", nil)
	first := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return isRed(boxColor(f.Result, "h1")) })
	if isBlue(boxColor(first.Result, "h2")) {
		t.Fatal("b.css must lose to a.css in document order")
	}
	if !strings.Contains(strings.Join(first.Warnings, "\n"), "b.css") {
		t.Fatalf("warnings %v", first.Warnings)
	}
	later := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return f.Err == nil && !isRed(boxColor(f.Result, "h1")) })
	if !isBlue(boxColor(later.Result, "h2")) {
		t.Fatalf("after the inline <style>, b.css (2,000) fits where a.css (3,000) does not; h2=%v", boxColor(later.Result, "h2"))
	}
}

func TestHeightOverflowRetriesWithoutImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(serve("image/png", pngBytes(t, 2, 2, color.RGBA{0, 0, 255, 255}))))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	// 200 sized images stack past nthtml's 16,384 px height cap; their alt
	// placeholders do not, so the retry without images must succeed.
	l.LoadPage(strings.Repeat(`<p><img src="/p.png" alt="big" width="10" height="100"></p>`, 200), srv.URL+"/", nil)
	f := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool {
		return f.Err == nil && strings.Contains(strings.Join(f.Warnings, "\n"), "without them")
	})
	if n := len(imgBoxes(f.Result)); n != 0 {
		t.Fatalf("%d image boxes survived the retry without images", n)
	}
}

func TestDynamicImgSrc(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		serve("image/png", pngBytes(t, 2, 2, color.RGBA{0, 0, 255, 255}))(w, r)
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<p><img id="i" alt="later"></p><script>setTimeout(() => document.getElementById("i").setAttribute("src", "/p.png"), 20)</script>`, srv.URL+"/", nil)
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool {
		b := imgBoxes(f.Result)
		return len(b) == 1 && b[0] == image.Pt(2, 2)
	})
	if h.get("/p.png") != 1 {
		t.Fatalf("p.png fetched %d times", h.get("/p.png"))
	}
}

func TestLoopLinkHrefSwap(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		switch r.URL.Path {
		case "/a.css":
			serve("text/css", []byte("h1{color:#ff0000}"))(w, r)
		case "/b.css":
			serve("text/css", []byte("h1{color:#00ff00}"))(w, r)
		}
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<link id="l" rel="stylesheet" href="/a.css"><h1>Hi</h1><script>setTimeout(() => document.getElementById("l").setAttribute("href", "/b.css"), 30)</script>`, srv.URL+"/", nil)
	first := nextFrame(t, sink)
	if !isRed(boxColor(first.Result, "h1")) {
		t.Fatalf("first frame not red: %v", first.Warnings)
	}
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return isGreen(boxColor(f.Result, "h1")) })
	if h.get("/a.css") != 1 || h.get("/b.css") != 1 {
		t.Fatalf("hits %v", h.m)
	}
}

func TestBackgroundImageFetchedAndPainted(t *testing.T) {
	cdnHits := newHits()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnHits.count(r)
		switch r.URL.Path {
		case "/css/t.css":
			serve("text/css", []byte(`.v{background:url("arrow.png");width:20px;height:20px;display:block} .s{background:url("dot.svg");width:20px;height:20px;display:block}`))(w, r)
		case "/css/arrow.png":
			serve("image/png", pngBytes(t, 2, 2, color.RGBA{0, 0, 255, 255}))(w, r)
		case "/css/dot.svg":
			serve("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><rect width="4" height="4" fill="#00ff00"/></svg>`))(w, r)
		}
	}))
	defer cdn.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer origin.Close()
	l, sink := newSubresLoop(t, subres.Config{Mode: subres.ModeAny})
	l.LoadPage(`<link rel="stylesheet" href="`+cdn.URL+`/css/t.css"><div id="v" class="v"></div><div id="s" class="s"></div><p>x</p>`, origin.URL+"/", nil)
	sample := func(res *document.Result, id string) (color.RGBA, bool) {
		pt, ok := res.Anchors[id]
		if !ok || res.Image == nil {
			return color.RGBA{}, false
		}
		c := res.Image.RGBAAt(pt.X+10, pt.Y+10)
		return c, true
	}
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool {
		v, ok1 := sample(f.Result, "v")
		s, ok2 := sample(f.Result, "s")
		return ok1 && ok2 && isBlue(v) && isGreen(s)
	})
	if cdnHits.get("/css/arrow.png") != 1 || cdnHits.get("/css/dot.svg") != 1 {
		t.Fatalf("hits %v", cdnHits.m)
	}
}

func TestDropPageCancelsSubresources(t *testing.T) {
	cancelled := make(chan struct{})
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		close(cancelled)
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<p><img src="/slow.png" alt="x"></p>`, srv.URL+"/", nil)
	nextFrame(t, sink)
	<-started
	l.LoadPage(`<p>second</p>`, srv.URL+"/", nil)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("dropPage did not cancel the in-flight subresource request")
	}
}

func TestFetchKindIsDocument(t *testing.T) {
	h := newHits()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		serve("application/json", []byte(`{"v":1}`))(w, r)
	}))
	defer srv.Close()
	origin, _ := url.Parse(srv.URL + "/")
	fetcher := netpolicy.New(netpolicy.Policy{Origin: origin, AllowPrivate: true})
	l, sink := newSubresLoop(t, subres.Config{})
	l.LoadPage(`<p id="p">wait</p><script>fetch("/data.json").then(r => r.json()).then(d => { document.getElementById("p").textContent = "got " + d.v; });</script>`, srv.URL+"/", fetcher)
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool {
		txt, _ := f.Result.TTY(context.Background(), 60)
		return strings.Contains(txt, "got 1")
	})
	if got := h.accept("/data.json"); got != "text/*, application/json;q=0.9" {
		t.Fatalf("fetch() Accept changed: %q", got)
	}
}

var _ tea.Msg = FrameMsg{}

// If nthtml refuses a page because of an injected sheet (the counter and the
// renderer disagree, or RenderLimit.Rules exceeds nthtml's 4096 ceiling), the
// sheets are dropped for the page and the next frame renders, never a
// permanently failed frame (decision 6's guarantee, independent of CountRules).
func TestRendererRefusalDropsInjectedSheets(t *testing.T) {
	// A library user lowered nthtml's input cap below the sheet's size: the
	// rule budget admits the sheet, the renderer refuses the page with it.
	srv := httptest.NewServer(http.HandlerFunc(serve("text/css", []byte("h1{color:#ff0000}/*"+strings.Repeat("x", 6000)+"*/"))))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 512)
	l := New(ctx, Config{Width: 400, MinFrame: time.Millisecond, RenderLimit: document.Limits{Bytes: 4096}, Subresources: subres.Config{}, SubresourceTransport: func(base string) subres.Transport {
		u, _ := url.Parse(base)
		return netpolicy.New(netpolicy.Policy{Origin: u, AllowPrivate: true, AnyOrigin: true, MaxBody: 1 << 20, MaxInFlight: 6})
	}}, sink)
	go l.Run()
	l.LoadPage(`<link rel="stylesheet" href="/huge.css"><h1>Hi</h1>`, srv.URL+"/", nil)
	// The drop is reported as a log line (it belongs to no frame) before the
	// unstyled frame that follows it.
	var f FrameMsg
	sawDrop := false
	deadline := time.After(5 * time.Second)
	for f.Result == nil {
		select {
		case m := <-sink:
			switch v := m.(type) {
			case LogMsg:
				if strings.Contains(v.Line, "huge.css") {
					sawDrop = true
				}
			case FrameMsg:
				if v.Err == nil {
					f = v
				}
			}
		case <-deadline:
			t.Fatal("no successful frame after the renderer refused the sheet")
		}
	}
	if !sawDrop {
		t.Fatal("no log line reported the dropped stylesheet")
	}
	if isRed(boxColor(f.Result, "h1")) {
		t.Fatal("the refused sheet is still applied")
	}
	// and the page keeps rendering afterwards without the sheet creeping back
	l.Resize(300)
	g := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return f.Err == nil && f.Result.Bounds().Dx() == 300 })
	if isRed(boxColor(g.Result, "h1")) {
		t.Fatal("the refused sheet was re-injected")
	}
}

// The first-frame deadline must not schedule a render of its own once the
// first frame is out: a quiet page gets exactly one frame.
func TestStyleDeadlineDoesNotRerender(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(serve("text/css", []byte("h1{color:#ff0000}"))))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{StyleTimeout: 200 * time.Millisecond})
	l.LoadPage(`<link rel="stylesheet" href="/s.css"><h1>Hi</h1>`, srv.URL+"/", nil)
	nextFrame(t, sink)
	frames := 0
	deadline := time.After(700 * time.Millisecond)
	for {
		select {
		case m := <-sink:
			if _, ok := m.(FrameMsg); ok {
				frames++
			}
			continue
		case <-deadline:
		}
		break
	}
	if frames != 0 {
		t.Fatalf("%d extra frame(s) after the first on a quiet page (the style deadline re-rendered)", frames)
	}
}
