package loop

import (
	"context"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/jsvm"
)

func pageState(t *testing.T, l *Loop) (base string, killed, disabled bool) {
	t.Helper()
	done := make(chan struct{})
	l.post(func() {
		if l.page != nil {
			base, killed, disabled = l.page.Base, l.page.killed, l.page.Sandbox.Disabled()
		}
		close(done)
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not answer")
	}
	return
}

// Two LoadPage calls in a row: the second always wins, whatever order the
// handoffs reach the VM goroutine in.
func TestLatestLoadPageWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := New(ctx, Config{Width: 200, Inbox: 1}, make(chanSink, 4096)) // tiny inbox forces the goroutine handoff path
	go l.Run()
	for i := 0; i < 50; i++ {
		l.LoadPage(`<title>A</title><p>a</p>`, "https://a.test/", nil)
		l.LoadPage(`<title>B</title><p>b</p>`, "https://b.test/", nil)
		// A may legitimately apply before B's call was even made; B must
		// apply eventually, and once it has, A must never apply again.
		deadline := time.Now().Add(5 * time.Second)
		for {
			base, _, _ := pageState(t, l)
			if base == "https://b.test/" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("round %d: B never applied (page=%q)", i, base)
			}
		}
		for k := 0; k < 3; k++ {
			if base, _, _ := pageState(t, l); base != "https://b.test/" {
				t.Fatalf("round %d: stale page won after B: %s", i, base)
			}
		}
	}
}

// preventDefault must see the link nthtml reports: href normalized the way
// nthtml normalizes it, label empty (image link) so Link.Text is the href.
func TestLinkActivatedMatchesNthtmlLinks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Width: 400, MinFrame: time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(`<!doctype html><body>
<a href="HTTP://example.test/A"><img alt=""></a>
<a href="/dup">one</a> <a href="/dup">two</a>
<script>
  for (const a of document.querySelectorAll("a")) a.addEventListener("click", e => { e.preventDefault(); console.log("prevented " + a.textContent); });
</script></body>`, "https://example.test/", nil)
	var frame FrameMsg
	deadline := time.After(5 * time.Second)
	for frame.Result == nil {
		select {
		case <-deadline:
			t.Fatal("no frame")
		case m := <-sink:
			if f, ok := m.(FrameMsg); ok && f.Err == nil {
				frame = f
			}
		}
	}
	if len(frame.Result.Links) != 3 {
		t.Fatalf("nthtml links: %+v", frame.Result.Links)
	}
	for _, link := range frame.Result.Links {
		l.LinkActivated(link)
	}
	prevented := 0
	timeout := time.After(2 * time.Second)
	for prevented < 3 {
		select {
		case <-timeout:
			t.Fatalf("only %d of 3 link clicks reached their listeners", prevented)
		case m := <-sink:
			switch v := m.(type) {
			case NavigateMsg:
				t.Fatalf("navigation not prevented: %s", v.Href)
			case LogMsg:
				if v.Level == "error" {
					t.Fatalf("page error: %s", v.Line)
				}
				if len(v.Line) > 9 && v.Line[:9] == "prevented" {
					prevented++
				}
			}
		}
	}
	// Duplicate hrefs resolve by label.
	for _, link := range frame.Result.Links {
		if link.Href == "/dup" && l.pageAnchorText(link) != link.Text {
			t.Fatalf("duplicate href %q resolved to the wrong element: %q", link.Text, l.pageAnchorText(link))
		}
	}
}

func (l *Loop) pageAnchorText(link document_Link) (text string) {
	done := make(chan struct{})
	l.post(func() {
		if n := l.anchorFor(link); n != nil {
			text = dom.TextContent(n)
		}
		close(done)
	})
	<-done
	return
}

// A page that exhausts its strike budget loses its runtime but not its
// frame: links still open, scripts never run again.
func TestDisabledPageIsDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Width: 200, Script: jsvm.Config{Slice: 5 * time.Millisecond, Strikes: 1}}, sink)
	go l.Run()
	l.LoadPage(`<body><a href="/go">go</a><script>
	  document.querySelector("a").addEventListener("click", e => e.preventDefault());
	  for(;;){}
	</script></body>`, "https://example.test/", nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, killed, disabled := pageState(t, l)
		if killed && disabled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("page not dropped: killed=%v disabled=%v", killed, disabled)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var frame FrameMsg
	timeout := time.After(5 * time.Second)
	for frame.Result == nil {
		select {
		case <-timeout:
			t.Fatal("no frame for the dropped page")
		case m := <-sink:
			if f, ok := m.(FrameMsg); ok && f.Err == nil {
				frame = f
			}
		}
	}
	l.LinkActivated(frame.Result.Links[0])
	timeout = time.After(2 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("link on a dropped page did not navigate")
		case m := <-sink:
			if n, ok := m.(NavigateMsg); ok {
				if n.Href != "https://example.test/go" {
					t.Fatalf("navigated to %s", n.Href)
				}
				return
			}
		}
	}
}

// A page loaded without a Fetcher gets deny-all, never a nil dereference on
// a worker goroutine (which would take the whole process down).
func TestNilFetcherDeniesInsteadOfPanicking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Width: 200, ExternalScripts: true}, sink)
	go l.Run()
	l.LoadPage(`<body><script src="https://x.test/a.js"></script><script>
	  fetch("https://x.test/d").catch(e => console.log("fetch refused " + (e instanceof Error)));
	</script></body>`, "https://example.test/", nil)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no refusal reported")
		case m := <-sink:
			if lm, ok := m.(LogMsg); ok && lm.Line == "fetch refused true" {
				return
			}
		}
	}
}
