package netpolicy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func openFetcher(t *testing.T, h http.HandlerFunc, p Policy) (*Fetcher, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	p.Origin, p.AllowPrivate = u, true
	return New(p), srv.URL
}

func TestOpenReturnsHeadersWithoutBody(t *testing.T) {
	release := make(chan struct{})
	f, base := openFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-release // the body comes only after Open has returned
		w.Write([]byte("hello"))
	}, Policy{})
	defer close(release)
	o, err := f.Open(context.Background(), Request{URL: base + "/v.mp4", Kind: KindNavigate})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Body.Close()
	if o.Status != 200 || o.ContentType != "video/mp4" || o.Length != 5 || !strings.HasSuffix(o.URL, "/v.mp4") {
		t.Fatalf("%+v", o)
	}
}

func TestOpenDocumentKindKeepsTypeCheck(t *testing.T) {
	f, base := openFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
	}, Policy{})
	if _, err := f.Open(context.Background(), Request{URL: base + "/v"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("%v", err)
	}
	if _, err := f.Do(context.Background(), Request{URL: base + "/v"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("Do: %v", err)
	}
}

func TestOpenKeepsRedirectAndAddressChecks(t *testing.T) {
	f, base := openFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest", http.StatusFound)
	}, Policy{})
	if _, err := f.Open(context.Background(), Request{URL: base + "/", Kind: KindNavigate}); err == nil {
		t.Fatal("redirect to a forbidden address followed")
	}
	u, _ := url.Parse("https://a.test/")
	strict := New(Policy{Origin: u})
	if _, err := strict.Open(context.Background(), Request{URL: "https://b.test/", Kind: KindNavigate}); !errors.Is(err, ErrDenied) {
		t.Fatalf("off-origin: %v", err)
	}
}

func TestOpenSlotHeldUntilClose(t *testing.T) {
	f, base := openFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write([]byte("x"))
	}, Policy{MaxInFlight: 1})
	o, err := f.Open(context.Background(), Request{URL: base + "/", Kind: KindNavigate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Open(context.Background(), Request{URL: base + "/", Kind: KindNavigate}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second open: %v", err)
	}
	data, _ := io.ReadAll(o.Body)
	if string(data) != "x" {
		t.Fatalf("%q", data)
	}
	o.Body.Close()
	o.Body.Close() // twice is safe
	deadline := time.Now().Add(time.Second)
	for {
		o2, err := f.Open(context.Background(), Request{URL: base + "/", Kind: KindNavigate})
		if err == nil {
			o2.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot not released: %v", err)
		}
	}
}

// A navigation's timeout covers reaching the headers; the caller owns the
// body, which may be a long download.
func TestNavigateBodyOutlivesHeaderTimeout(t *testing.T) {
	f, base := openFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		for range 4 {
			w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			time.Sleep(60 * time.Millisecond)
		}
	}, Policy{Timeout: 100 * time.Millisecond})
	o, err := f.Open(context.Background(), Request{URL: base + "/", Kind: KindNavigate})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Body.Close()
	data, err := io.ReadAll(o.Body)
	if err != nil || len(data) != 4 {
		t.Fatalf("%d bytes, %v", len(data), err)
	}
}

// A host's first address may be unreachable (IPv6 with no route) while the
// next works.
func TestDialTriesEveryVettedAddress(t *testing.T) {
	var tried []string
	dial := func(_ context.Context, _, addr string) (net.Conn, error) {
		tried = append(tried, addr)
		if len(tried) < 2 {
			return nil, errors.New("no route to host")
		}
		c, _ := net.Pipe()
		return c, nil
	}
	ips := []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("192.0.2.1")}
	c, err := dialVetted(context.Background(), dial, "tcp", ips, "443")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if len(tried) != 2 || tried[1] != "192.0.2.1:443" {
		t.Fatalf("%v", tried)
	}
	if _, err := dialVetted(context.Background(), func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("down")
	}, "tcp", ips, "443"); err == nil {
		t.Fatal("all addresses failed but no error")
	}
}
