package subres

import (
	"context"
	"errors"
	"fmt"
	"image"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"nimbleterminal.dev/ntbrowser/netpolicy"
)

const maxImportDepth = 3

type state uint8

const (
	pending state = iota
	loaded
	failed
)

type sheetEntry struct {
	state    state
	blocking bool // discovered before the first frame (decision 3)
	css      string
	rules    int
}

type imageEntry struct {
	state state
	img   image.Image
}

type job struct {
	key        string // raw key as discovered
	abs        string // absolute URL, "" for a data: image
	sheet      bool
	background bool
}

// Session is one page's subresources (decision 11: nothing outlives the
// page). Every exported method runs on the goroutine that owns the session;
// workers only call the transport, decode, and post their result back.
type Session struct {
	ctx    context.Context
	cancel context.CancelFunc
	cfg    Config
	t      Transport
	base   *url.URL
	post   func(func())

	sheets      map[string]*sheetEntry
	images      map[string]*imageEntry
	backgrounds map[string]*imageEntry
	queue       []job
	inflight    int

	sheetCount     atomic.Int32 // sheets requested, imports included
	imageCount     int
	pixels         int
	warnings       []string
	warnedSheetCap bool
	warnedImageCap bool
	firstFrameDone bool
	closed         bool
}

// NewSession starts a session for the page at base. post hands a completion
// to the owning goroutine; the loop wraps it to drop stale completions and
// mark the tree dirty.
func NewSession(ctx context.Context, cfg Config, t Transport, base string, post func(func())) *Session {
	cfg = cfg.WithDefaults()
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{
		ctx: ctx, cancel: cancel, cfg: cfg, t: t,
		sheets: map[string]*sheetEntry{}, images: map[string]*imageEntry{}, backgrounds: map[string]*imageEntry{},
	}
	s.base, _ = url.Parse(base)
	s.post = func(fn func()) {
		if ctx.Err() != nil {
			return
		}
		post(func() {
			if ctx.Err() != nil {
				return
			}
			fn()
		})
	}
	return s
}

// Discover requests every key not yet seen: sheets in document order, then
// images, then CSS background sources. ModeOff requests nothing.
func (s *Session) Discover(sheets, images, backgrounds []string) {
	if s.closed || s.cfg.Mode == ModeOff {
		return
	}
	for _, href := range sheets {
		if _, seen := s.sheets[href]; seen {
			continue
		}
		e := &sheetEntry{blocking: !s.firstFrameDone}
		s.sheets[href] = e
		abs, err := s.resolve(href)
		switch {
		case err != nil:
			e.state = failed
			s.warn("stylesheet %q: %v", href, err)
		case strings.HasPrefix(strings.ToLower(abs), "data:"):
			e.state = failed
			s.warn("stylesheet %q: data: URLs are not stylesheets", href)
		case int(s.sheetCount.Add(1)) > s.cfg.MaxStylesheets:
			e.state = failed
			if !s.warnedSheetCap {
				s.warnedSheetCap = true
				s.warn("more than %d stylesheets; the rest are ignored", s.cfg.MaxStylesheets)
			}
		default:
			s.queue = append(s.queue, job{key: href, abs: abs, sheet: true})
		}
	}
	for _, src := range images {
		s.discoverImage(src, false)
	}
	for _, src := range backgrounds {
		if strings.HasPrefix(strings.ToLower(src), "data:") {
			continue // nthtml decodes data: backgrounds itself (decision 12)
		}
		s.discoverImage(src, true)
	}
	s.pump()
}

func (s *Session) discoverImage(key string, background bool) {
	m := s.images
	if background {
		m = s.backgrounds
	}
	if _, seen := m[key]; seen {
		return
	}
	e := &imageEntry{}
	m[key] = e
	if s.imageCount >= s.cfg.MaxImages {
		e.state = failed
		if !s.warnedImageCap {
			s.warnedImageCap = true
			s.warn("more than %d images; the rest are ignored", s.cfg.MaxImages)
		}
		return
	}
	s.imageCount++
	if strings.HasPrefix(strings.ToLower(key), "data:") {
		s.queue = append(s.queue, job{key: key, background: background})
		return
	}
	abs, err := s.resolve(key)
	if err != nil {
		e.state = failed
		s.warn("image %q: %v", key, err)
		return
	}
	s.queue = append(s.queue, job{key: key, abs: abs, background: background})
}

func (s *Session) resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("empty reference")
	}
	u, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(u.Scheme, "data") {
		return ref, nil
	}
	if s.base != nil {
		u = s.base.ResolveReference(u)
	}
	u.Fragment = ""
	return u.String(), nil
}

func (s *Session) warn(format string, args ...any) {
	s.warnings = append(s.warnings, fmt.Sprintf(format, args...))
}

// pump starts workers up to MaxInFlight.
func (s *Session) pump() {
	for !s.closed && s.inflight < s.cfg.MaxInFlight && len(s.queue) > 0 {
		j := s.queue[0]
		s.queue = s.queue[1:]
		s.inflight++
		go s.work(j)
	}
}

func (s *Session) work(j job) {
	switch {
	case j.sheet:
		css, warns, ok := s.fetchSheet(j.abs, 0, nil)
		rules := 0
		if ok {
			rules = CountRules(css)
		}
		s.post(func() {
			s.inflight--
			e := s.sheets[j.key]
			for _, w := range warns {
				s.warn("stylesheet %q: %s", j.key, w)
			}
			if ok {
				e.state, e.css, e.rules = loaded, css, rules
			} else {
				e.state = failed
			}
			s.pump()
		})
	default:
		var img image.Image
		var err error
		if j.abs == "" {
			ctx, cancel := context.WithTimeout(s.ctx, s.cfg.Timeout)
			img, err = decodeDataURL(ctx, j.key)
			cancel()
		} else {
			img, err = s.fetchImage(j.abs)
		}
		s.post(func() {
			s.inflight--
			m := s.images
			if j.background {
				m = s.backgrounds
			}
			e := m[j.key]
			switch {
			case err != nil:
				e.state = failed
				s.warn("image %q: %v", j.key, err)
			default:
				b := img.Bounds()
				px := b.Dx() * b.Dy()
				if s.pixels+px > s.cfg.MaxImagePixels {
					e.state = failed
					s.warn("image %q: over the page's pixel budget", j.key)
				} else {
					s.pixels += px
					e.state, e.img = loaded, img
				}
			}
			s.pump()
		})
	}
}

// do performs one request with the per-request timeout, retrying a busy
// fetcher once.
func (s *Session) do(r netpolicy.Request) (netpolicy.Response, error) {
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(s.ctx, s.cfg.Timeout)
		resp, err := s.t.Do(ctx, r)
		cancel()
		if errors.Is(err, netpolicy.ErrBusy) && attempt == 0 {
			select {
			case <-time.After(50 * time.Millisecond):
				continue
			case <-s.ctx.Done():
				return resp, s.ctx.Err()
			}
		}
		return resp, err
	}
}

// fetchSheet loads one stylesheet, absolutizes its url() references against
// its final URL, and splices its leading @imports (decision 17).
func (s *Session) fetchSheet(abs string, depth int, chain []string) (css string, warns []string, ok bool) {
	resp, err := s.do(netpolicy.Request{Method: "GET", URL: abs, Kind: netpolicy.KindStyle})
	if err != nil {
		return "", []string{err.Error()}, false
	}
	if len(resp.Body) > s.cfg.MaxSheetBytes {
		return "", []string{fmt.Sprintf("stylesheet too large (%d bytes, limit %d)", len(resp.Body), s.cfg.MaxSheetBytes)}, false
	}
	final := resp.URL
	if final == "" {
		final = abs
	}
	text := Absolutize(resp.Body, final)
	stmts, _ := Imports(text)
	if len(stmts) == 0 {
		return text, nil, true
	}
	chain = append(chain, final)
	var out strings.Builder
	last := 0
	for _, im := range stmts {
		start, end := im.Range()
		out.WriteString(text[last:start])
		last = end
		switch {
		case im.Unsupported:
			warns = append(warns, fmt.Sprintf("@import %q with layer()/supports() dropped", im.URL))
			continue
		case depth >= maxImportDepth:
			warns = append(warns, fmt.Sprintf("@import %q dropped: depth over %d", im.URL, maxImportDepth))
			continue
		}
		target, err := resolveAgainst(final, im.URL)
		if err != nil {
			warns = append(warns, fmt.Sprintf("@import %q dropped: %v", im.URL, err))
			continue
		}
		cyclic := false
		for _, c := range chain {
			if c == target {
				cyclic = true
				break
			}
		}
		if cyclic {
			warns = append(warns, fmt.Sprintf("@import %q dropped: import cycle", im.URL))
			continue
		}
		if int(s.sheetCount.Add(1)) > s.cfg.MaxStylesheets {
			warns = append(warns, fmt.Sprintf("@import %q dropped: more than %d stylesheets", im.URL, s.cfg.MaxStylesheets))
			continue
		}
		sub, subWarns, subOK := s.fetchSheet(target, depth+1, chain)
		warns = append(warns, subWarns...)
		if !subOK {
			warns = append(warns, fmt.Sprintf("@import %q failed", im.URL))
			continue
		}
		if im.Media != "" {
			out.WriteString("@media ")
			out.WriteString(im.Media)
			out.WriteString(" {\n")
			out.WriteString(sub)
			out.WriteString("\n}\n")
		} else {
			out.WriteString(sub)
			out.WriteString("\n")
		}
	}
	out.WriteString(text[last:])
	return out.String(), warns, true
}

func resolveAgainst(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	u = b.ResolveReference(u)
	u.Fragment = ""
	return u.String(), nil
}

func (s *Session) fetchImage(abs string) (image.Image, error) {
	resp, err := s.do(netpolicy.Request{Method: "GET", URL: abs, Kind: netpolicy.KindImage})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.Timeout)
	defer cancel()
	return decodeFetched(ctx, resp.Headers["content-type"], resp.Body)
}

// FirstFrameRendered closes the blocking set: sheets discovered from now on
// never hold a frame (decision 3).
func (s *Session) FirstFrameRendered() { s.firstFrameDone = true }

// StylesPending reports whether a blocking stylesheet is still unsettled.
func (s *Session) StylesPending() bool {
	for _, e := range s.sheets {
		if e.blocking && e.state == pending {
			return true
		}
	}
	return false
}

// Style returns the loaded, spliced, absolutized text for href and its rule
// count; ok is false while pending or after a failure.
func (s *Session) Style(href string) (css string, rules int, ok bool) {
	e := s.sheets[href]
	if e == nil || e.state != loaded {
		return "", 0, false
	}
	return e.css, e.rules, true
}

// Images returns a fresh copy of the decoded <img> sources.
func (s *Session) Images() map[string]image.Image { return snapshot(s.images) }

// BackgroundImages returns a fresh copy of the decoded CSS background sources,
// keyed by the url() text as written (decision 21).
func (s *Session) BackgroundImages() map[string]image.Image { return snapshot(s.backgrounds) }

func snapshot(m map[string]*imageEntry) map[string]image.Image {
	out := make(map[string]image.Image, len(m))
	for k, e := range m {
		if e.state == loaded && e.img != nil {
			out[k] = e.img
		}
	}
	return out
}

// Warnings drains the pending warnings.
func (s *Session) Warnings() []string {
	w := s.warnings
	s.warnings = nil
	return w
}

// Close cancels in-flight work; later Discover calls are inert.
func (s *Session) Close() {
	s.closed = true
	s.cancel()
}
