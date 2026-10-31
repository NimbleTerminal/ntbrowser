package loop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/netpolicy"
)

type chanSink chan tea.Msg

func (c chanSink) Send(m tea.Msg) { c <- m }

const page = `<!doctype html><html><head><title>Loop test</title></head><body>
<h1 id="h">Hello</h1><p id="p">waiting</p><a id="lnk" href="https://example.com/next">next</a>
<a id="rel" href="../other.html">other</a>
<script>
  document.getElementById("lnk").addEventListener("click", e => e.preventDefault());
  requestAnimationFrame(ts => console.log("raf", typeof ts === "number" && ts >= 0));
  setTimeout(() => { document.getElementById("h").textContent = "Tick"; }, 5);
  fetch("/data.json").then(r => r.json()).then(d => {
    document.getElementById("p").textContent = "got " + d.value;
  }).catch(e => console.error("fetch failed", String(e)));
  console.log("booted", 1 + 1);
</script></body></html>`

func TestLoopEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value":42}`))
	}))
	defer srv.Close()
	origin, _ := url.Parse(srv.URL + "/page.html")
	fetcher := netpolicy.New(netpolicy.Policy{Origin: origin, AllowPrivate: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	l := New(ctx, Config{Profile: testProfile, Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(page, origin.String(), fetcher)

	var sawTitle, sawBoot, sawTick, sawFetch, sawRAF bool
	deadline := time.After(5 * time.Second)
	for !(sawTitle && sawBoot && sawTick && sawFetch && sawRAF) {
		select {
		case <-deadline:
			t.Fatalf("timeout: title=%v boot=%v tick=%v fetch=%v raf=%v", sawTitle, sawBoot, sawTick, sawFetch, sawRAF)
		case m := <-sink:
			switch v := m.(type) {
			case TitleMsg:
				sawTitle = v.Title == "Loop test"
			case LogMsg:
				if v.Level == "error" {
					t.Fatalf("page error: %s", v.Line)
				}
				sawBoot = sawBoot || v.Line == "booted 2"
				sawRAF = sawRAF || v.Line == "raf true"
			case FrameMsg:
				if v.Err != nil {
					t.Fatal(v.Err)
				}
				text, _ := v.Result.TTY(ctx, 60)
				sawTick = sawTick || strings.Contains(text, "Tick")
				sawFetch = sawFetch || strings.Contains(text, "got 42")
			}
		}
	}
	// Link default action is cancelled by the page's listener …
	l.LinkActivated(mustLink(t, l))
	select {
	case m := <-sink:
		if _, nav := m.(NavigateMsg); nav {
			t.Fatal("navigation should have been prevented")
		}
	case <-time.After(200 * time.Millisecond):
	}
	// A relative link resolves against the page base before it reaches the host.
	l.LinkActivated(document_Link{Href: "../other.html", Text: "other"})
	select {
	case m := <-sink:
		nav, ok := m.(NavigateMsg)
		if !ok || nav.Href != srv.URL+"/other.html" {
			t.Fatalf("expected resolved NavigateMsg, got %#v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no NavigateMsg for relative link")
	}
	// … but a hostile script cannot block the terminal: the watchdog trips.
	l.post(func() { l.report(l.page.Sandbox.Eval("spin", "for(;;){}")) })
	select {
	case m := <-sink:
		if lm, ok := m.(LogMsg); !ok || !strings.Contains(lm.Line, "time slice") {
			t.Fatalf("expected watchdog log, got %#v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not fire")
	}
}

func mustLink(t *testing.T, l *Loop) (link document_Link) {
	done := make(chan document_Link)
	l.post(func() {
		anchors, _ := l.page.Tree.Query(l.page.Tree.Root(), "a", true)
		href, _ := anchors[0].Attr("href")
		done <- document_Link{Href: href, Text: "next"}
	})
	return <-done
}
