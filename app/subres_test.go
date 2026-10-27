package app

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/subres"
)

func TestSubresourceTransportPolicy(t *testing.T) {
	base := netpolicy.Policy{AllowHosts: []string{".cdn.test"}, AllowPrivate: true, MaxRedirects: 5}
	p, err := policyFor(base, subres.Config{Mode: subres.ModeAny}, "https://example.test/a/b.html")
	if err != nil {
		t.Fatal(err)
	}
	if p.Origin == nil || p.Origin.Host != "example.test" || p.Origin.Scheme != "https" {
		t.Fatalf("origin %v", p.Origin)
	}
	if !p.AnyOrigin || p.NoDowngradeFrom != "https" {
		t.Fatalf("ModeAny https: AnyOrigin=%v NoDowngradeFrom=%q", p.AnyOrigin, p.NoDowngradeFrom)
	}
	if len(p.AllowHosts) != 1 || p.AllowHosts[0] != ".cdn.test" || !p.AllowPrivate || p.MaxRedirects != 5 {
		t.Fatalf("policy fields not copied: %+v", p)
	}
	if p.MaxBody != 1<<20 || p.MaxInFlight != 6 || p.Timeout != 10*time.Second {
		t.Fatalf("defaults: MaxBody=%d MaxInFlight=%d Timeout=%v", p.MaxBody, p.MaxInFlight, p.Timeout)
	}
	p, err = policyFor(base, subres.Config{Mode: subres.ModeOrigin, MaxSheetBytes: 512 << 10, MaxInFlight: 12, Timeout: 3 * time.Second}, "http://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	if p.AnyOrigin || p.NoDowngradeFrom != "http" {
		t.Fatalf("ModeOrigin http: AnyOrigin=%v NoDowngradeFrom=%q", p.AnyOrigin, p.NoDowngradeFrom)
	}
	if p.MaxBody != 1<<20 || p.MaxInFlight != 12 || p.Timeout != 3*time.Second {
		t.Fatalf("raised config: MaxBody=%d MaxInFlight=%d Timeout=%v", p.MaxBody, p.MaxInFlight, p.Timeout)
	}
	p, _ = policyFor(base, subres.Config{MaxSheetBytes: 2 << 20}, "http://example.test/")
	if p.MaxBody != 2<<20 {
		t.Fatalf("MaxBody must follow a larger MaxSheetBytes: %d", p.MaxBody)
	}
	if _, err := policyFor(base, subres.Config{}, "::"); err == nil {
		t.Fatal("unparsable base must error")
	}
	// A file:// start page (the documented local case) still loads remote
	// subresources under ModeAny; relative refs resolve to file: and are
	// refused by netpolicy's scheme check, so nothing local is ever read.
	p, err = policyFor(base, subres.Config{Mode: subres.ModeAny}, "file:///tmp/page.html")
	if err != nil {
		t.Fatal(err)
	}
	if p.Origin != nil || !p.AnyOrigin || p.NoDowngradeFrom != "" {
		t.Fatalf("file base: %+v", p)
	}
	f := netpolicy.New(p)
	if err := f.Allowed(mustURL(t, "https://cdn.test/s.css")); err != nil {
		t.Fatalf("file page must reach a CDN sheet under ModeAny: %v", err)
	}
	if err := f.Allowed(mustURL(t, "file:///etc/passwd")); err == nil {
		t.Fatal("file: refs must stay refused")
	}
}

func TestFrameWarningsReachStatus(t *testing.T) {
	res, err := document.Render(context.Background(), strings.NewReader(`<p>one line</p>`), document.Options{Width: 400})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Width: 400}, dropSink{})
	go l.Run()
	m := New(l, netpolicy.Policy{}, NavFree, "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(loop.FrameMsg{Gen: 1, Result: res, Warnings: []string{"stylesheet \"x.css\": denied"}})
	if !strings.Contains(m.status, `stylesheet "x.css"`) {
		t.Fatalf("status %q", m.status)
	}
}
