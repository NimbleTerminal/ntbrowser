// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
// Package compare implements a developer-only browser comparison host.
// It does not add a capture API, listener, or filesystem authority to pages.
package compare

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/app"
	"nimbleterminal.dev/ntbrowser/cookies"
	"nimbleterminal.dev/ntbrowser/diagnostics"
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/internal/browsercmd"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/subres"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
)

type Options struct {
	URL, Out, Chrome, Scheme string
	Width, Height            int
	Scripts                  bool
	Settle                   time.Duration
}
type Source struct {
	URL, HTML, ContentType string
	Status                 int
	Headers                map[string]string
}
type Rect struct{ X, Y, Width, Height float64 }
type Element struct {
	ID, Tag, Text                       string
	Rect                                Rect
	Display, Font, FontSize, LineHeight string
}
type Event struct {
	Kind string
	Data json.RawMessage
}
type Capture struct {
	Engine, URL, Screenshot, Text string
	Width, Height                 int
	Elements                      []Element
	Boxes                         []document.Box `json:",omitempty"`
	Warnings                      []string
	Events                        []Event
	Error                         string `json:",omitempty"`
	DroppedEvents                 int
	MissingGlyphs                 int
}
type Geometry struct {
	ID, Status string
	Chrome     Rect
	Glynx      *Rect `json:",omitempty"`
	Note       string
}
type Report struct {
	Version                 int
	Created                 time.Time
	Options                 Options
	SourceURL, SourceSHA256 string
	SourceStatus            int
	Build                   string
	Note                    string
	Glynx, Chrome           Capture
	Geometry                []Geometry
	DifferentPixels         int
	PixelDifferencePercent  float64
}

func (o Options) validate() error {
	u, e := url.Parse(o.URL)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return errors.New("URL must be an HTTP(S) address without credentials")
	}
	if o.Width < 320 || o.Width > 2048 || o.Height < 200 || o.Height > 2048 {
		return errors.New("viewport must be 320..2048 wide and 200..2048 high")
	}
	if o.Scheme != "light" && o.Scheme != "dark" {
		return errors.New("scheme must be light or dark")
	}
	if o.Settle < 100*time.Millisecond || o.Settle > 15*time.Second {
		return errors.New("settle must be 100ms..15s")
	}
	if o.Out == "" {
		return errors.New("output directory is required")
	}
	return nil
}
func Run(ctx context.Context, o Options) (Report, error) {
	r := Report{Version: 1, Created: time.Now().UTC(), Options: o, Note: "Live comparison: identical initial HTML; assets, scripts and cookies are fetched independently. Fresh sessions, no saved credentials. Glynx uses its loop and published renderer, without terminal transport or OS confinement. Chrome uses its sandbox. Captures are after a fixed settling interval, not proof of network idle. Geometry compares ID-associated painted coverage, not necessarily CSS border boxes. Pixel differences include fonts and antialiasing."}
	if e := o.validate(); e != nil {
		return r, e
	}
	if _, e := findChrome(o.Chrome); e != nil {
		return r, e
	}
	// New directory only: never overwrite a previous capture or user files.
	if e := os.Mkdir(o.Out, 0700); e != nil {
		return r, e
	}
	if b, ok := debug.ReadBuildInfo(); ok {
		r.Build = b.String()
	}
	source, e := netpolicy.New(netpolicy.Policy{AnyOrigin: true, MaxBody: int64(browsercmd.Glynx.PageBytes)}).Do(ctx, netpolicy.Request{URL: o.URL, Kind: netpolicy.KindNavigate})
	if e != nil {
		return r, e
	}
	ct := source.Headers["content-type"]
	if !strings.HasPrefix(strings.ToLower(ct), "text/html") {
		return r, fmt.Errorf("comparison currently requires text/html, received %q", ct)
	}
	src := Source{URL: source.URL, HTML: source.Body, Status: source.Status, ContentType: ct, Headers: source.Headers}
	r.SourceURL = src.URL
	r.SourceStatus = src.Status
	r.SourceSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(src.HTML)))
	if e = os.WriteFile(filepath.Join(o.Out, "source.html"), []byte(src.HTML), 0600); e != nil {
		return r, e
	}
	r.Glynx, e = captureGlynx(ctx, o, src)
	if e != nil {
		r.Glynx.Error = e.Error()
	}
	r.Chrome, e = captureChrome(ctx, o, src)
	if e != nil {
		r.Chrome.Error = e.Error()
	}
	r.Geometry = geometry(r.Glynx.Boxes, r.Chrome.Elements)
	if r.Glynx.Error == "" && r.Chrome.Error == "" {
		n, err := writeDiff(o)
		if err != nil {
			return r, err
		}
		r.DifferentPixels = n
		r.PixelDifferencePercent = 100 * float64(n) / float64(o.Width*o.Height)
	}
	if e = writeReport(o.Out, r); e != nil {
		return r, e
	}
	if r.Glynx.Error != "" || r.Chrome.Error != "" {
		return r, errors.New("capture incomplete; inspect report.json")
	}
	return r, nil
}

type captureSink struct {
	ctx context.Context
	ch  chan tea.Msg
}

func (s captureSink) Send(m tea.Msg) {
	select {
	case s.ch <- m:
	case <-s.ctx.Done():
	}
}
func captureGlynx(ctx context.Context, o Options, src Source) (Capture, error) {
	r := Capture{Engine: "glynx loop (published nthtml)", URL: src.URL}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan Event, 2048)
	var dropped atomic.Int64
	trace := diagnostics.NewObserved(nil, func(kind string, v any) {
		// Snapshot HTML is already bounded by the DOM; omit it from the event list.
		if kind == "snapshot" || kind == "document" {
			return
		}
		b, e := json.Marshal(v)
		if e != nil || len(b) > 64<<10 {
			dropped.Add(1)
			return
		}
		select {
		case events <- Event{Kind: kind, Data: b}:
		default:
			dropped.Add(1)
		}
	})
	defer trace.Close()
	b := browsercmd.Glynx
	p, _ := profile.Builtin(b.Profile)
	p.Persist = profile.PersistOff
	p.Diagnostics = true
	if !o.Scripts {
		p.Scripts = profile.ScriptsOff
	}
	pool := netpolicy.NewPool()
	defer pool.Close()
	policy := app.PolicyFor(p)
	policy.Pool = pool
	policy.Trace = trace
	policy.Cookies = cookies.New()
	sc := subres.Config{Mode: subres.ModeAny, MaxSheetBytes: b.StylesheetBytes, MaxStylesheets: b.Stylesheets, MaxImageBytes: b.ImageBytes, MaxImages: b.Images}
	sink := captureSink{ctx: ctx, ch: make(chan tea.Msg, 256)}
	l := loop.New(ctx, loop.Config{Profile: p, Width: o.Width, ColorScheme: o.Scheme, Trace: trace, MinFrame: 16 * time.Millisecond, Script: jsvm.Config{HeapBudget: b.HeapBudgetMiB << 20}, DOMLimits: dom.Limits{ExternalStyleBytes: b.StylesheetTotalBytes, TotalBytes: b.PageBytes}, RenderLimit: document.Limits{Rules: b.CSSRules}, Subresources: sc, SubresourceTransport: app.SubresourceTransport(policy, sc)}, sink)
	// Exact CSS-pixel viewport; terminal cell geometry and Kitty transport are not compared.
	l.SetViewport(nthtml.PaintViewport{Cols: o.Width, Rows: o.Height, CellWidth: 1, CellHeight: 1, MaxRasterPixels: o.Width * o.Height * 2})
	go l.Run()
	defer func() { cancel(); <-l.Done() }()
	l.SetScale(1)
	u, e := url.Parse(src.URL)
	if e != nil {
		return r, e
	}
	policy.Origin = u
	l.LoadPage(src.HTML, src.URL, netpolicy.New(policy))
	var latest loop.FrameMsg
	timer := time.NewTimer(o.Settle)
	timer.Stop()
	defer timer.Stop()
	settled := false
	for !settled || latest.Result == nil {
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-timer.C:
			settled = true
		case ev := <-events:
			if len(r.Events) < 2048 {
				r.Events = append(r.Events, ev)
			} else {
				r.DroppedEvents++
			}
		case msg := <-sink.ch:
			switch m := msg.(type) {
			case loop.FrameMsg:
				if m.Err != nil {
					r.Warnings = append(r.Warnings, m.Err.Error())
					continue
				}
				if m.Result != nil && m.Painted != nil {
					if latest.Result == nil {
						timer.Reset(o.Settle)
					}
					latest = m
				}
			case loop.LogMsg:
				if len(r.Warnings) < 256 {
					r.Warnings = append(r.Warnings, m.Level+": "+m.Line)
				}
				if strings.HasPrefix(m.Line, "parse:") {
					return r, errors.New(m.Line)
				}
			case loop.ScriptNavigateMsg:
				r.Warnings = append(r.Warnings, "script navigation was not followed: "+m.Href)
			}
		}
	}
	// Freeze capture inputs before shutdown; they are immutable render results.
	// Ask the same serial painter for the exact viewport, avoiding the terminal
	// band's row/column ceilings. Keep the result-generation check intact.
	request := func() {
		area := image.Rect(0, 0, o.Width, o.Height).Intersect(latest.Result.Bounds())
		l.Paint(nthtml.PaintRequest{Result: latest.Result, Revision: latest.Gen, Sequence: 1, Context: ctx, Plan: nthtml.PaintPlan{Area: area, Scale: 1}})
	}
	request()
	var raster image.Image
	for raster == nil {
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case msg := <-sink.ch:
			switch m := msg.(type) {
			case loop.FrameMsg:
				if m.Err == nil && m.Result != nil && m.Painted != nil {
					latest = m
					request()
				}
			case nthtml.PaintReady:
				if m.Request.Result != latest.Result {
					continue
				}
				if m.Err != nil {
					return r, m.Err
				}
				raster = m.Image
			}
		}
	}
	shot := image.NewRGBA(image.Rect(0, 0, o.Width, o.Height))
	draw.Draw(shot, shot.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(shot, shot.Bounds(), raster, image.Point{}, draw.Src)
	r.Width = latest.Result.Bounds().Dx()
	r.Height = latest.Result.Bounds().Dy()
	r.Warnings = append(r.Warnings, latest.Warnings...)
	r.Warnings = append(r.Warnings, latest.Result.Warnings...)
	r.Boxes = latest.Result.Boxes
	r.MissingGlyphs = latest.Result.MissingGlyphs
	r.Text, e = latest.Result.TTY(ctx, max(32, o.Width/10))
	if e != nil {
		return r, e
	}
drain:
	for {
		select {
		case ev := <-events:
			if len(r.Events) < 2048 {
				r.Events = append(r.Events, ev)
			} else {
				r.DroppedEvents++
			}
		default:
			break drain
		}
	}
	r.DroppedEvents += int(dropped.Load())
	r.Screenshot = "glynx.png"
	return r, writePNG(filepath.Join(o.Out, r.Screenshot), shot)
}
func rect(r image.Rectangle) Rect {
	return Rect{float64(r.Min.X), float64(r.Min.Y), float64(r.Dx()), float64(r.Dy())}
}
func geometry(boxes []document.Box, elements []Element) []Geometry {
	var result []Geometry
	counts := map[string]int{}
	for _, el := range elements {
		counts[el.ID]++
	}
	for _, el := range elements {
		if el.ID == "" {
			continue
		}
		g := Geometry{ID: el.ID, Chrome: el.Rect, Status: "UNAVAILABLE", Note: "No non-empty ID-associated painted box; this is not proof the element is absent."}
		if counts[el.ID] > 1 {
			g.Note = "Duplicate ID; not matched."
			result = append(result, g)
			continue
		}
		var bounds image.Rectangle
		for _, b := range boxes {
			if b.Tag != "anchor" && (b.Anchor == el.ID || b.SourceID == el.ID) && !b.Bounds.Empty() {
				bounds = bounds.Union(b.Bounds)
			}
		}
		if !bounds.Empty() {
			v := rect(bounds)
			g.Glynx = &v
			g.Status = "COVERAGE_DIFFERS"
			g.Note = "Painted coverage versus Chrome border box; inspect screenshots before classifying."
			if v == el.Rect {
				g.Status = "COVERAGE_MATCH"
			}
		}
		result = append(result, g)
	}
	return result
}
func writePNG(path string, img image.Image) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	e = png.Encode(f, img)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func writeDiff(o Options) (int, error) {
	read := func(name string) (image.Image, error) {
		f, e := os.Open(filepath.Join(o.Out, name))
		if e != nil {
			return nil, e
		}
		defer f.Close()
		return png.Decode(f)
	}
	a, e := read("glynx.png")
	if e != nil {
		return 0, e
	}
	b, e := read("chrome.png")
	if e != nil {
		return 0, e
	}
	want := image.Rect(0, 0, o.Width, o.Height)
	if a.Bounds() != want || b.Bounds() != want {
		return 0, fmt.Errorf("screenshot size mismatch: %v / %v, wanted %v", a.Bounds(), b.Bounds(), want)
	}
	out := image.NewRGBA(want)
	n := 0
	for y := 0; y < o.Height; y++ {
		for x := 0; x < o.Width; x++ {
			ca := color.NRGBAModel.Convert(a.At(x, y))
			cb := color.NRGBAModel.Convert(b.At(x, y))
			if ca != cb {
				n++
				out.Set(x, y, color.RGBA{255, 0, 255, 255})
			} else {
				out.Set(x, y, color.White)
			}
		}
	}
	return n, writePNG(filepath.Join(o.Out, "diff.png"), out)
}
