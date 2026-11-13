// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package compare

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/nthtml/document"
)

func testOptions(t *testing.T) Options {
	return Options{URL: "https://example.test/", Out: t.TempDir(), Width: 1200, Height: 900, Scheme: "light", Scripts: true, Settle: 100 * time.Millisecond}
}
func TestOptionsRejectUnsafeOrUnboundedInput(t *testing.T) {
	for _, change := range []func(*Options){func(o *Options) { o.URL = "file:///etc/passwd" }, func(o *Options) { o.URL = "https://user:pass@example.test" }, func(o *Options) { o.Width = 100000 }, func(o *Options) { o.Height = -1 }, func(o *Options) { o.Settle = time.Hour }, func(o *Options) { o.Scheme = "auto" }} {
		o := testOptions(t)
		change(&o)
		if o.validate() == nil {
			t.Fatal("accepted invalid options", o)
		}
	}
}
func TestGeometryDoesNotGuessMissingOrDuplicateIDs(t *testing.T) {
	boxes := []document.Box{{Anchor: "marker", Tag: "anchor", Bounds: image.Rect(2, 3, 3, 4)}, {SourceID: "item", Bounds: image.Rect(10, 20, 30, 40)}}
	els := []Element{{ID: "marker"}, {ID: "item", Rect: Rect{10, 20, 20, 20}}, {ID: "duplicate"}, {ID: "duplicate"}, {Tag: "h1"}}
	g := geometry(boxes, els)
	if len(g) != 4 || g[0].Status != "UNAVAILABLE" || g[1].Status != "COVERAGE_MATCH" || g[2].Status != "UNAVAILABLE" {
		t.Fatalf("bad geometry: %+v", g)
	}
}
func TestReportEscapesPageContentAndNeverEmbedsSource(t *testing.T) {
	dir := t.TempDir()
	attack := `<script>alert(1)</script>`
	if e := writeReport(dir, Report{SourceURL: attack, Glynx: Capture{Text: attack, Warnings: []string{attack}}}); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(dir, "index.html"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), attack) || !strings.Contains(string(data), "&lt;script&gt;") {
		t.Fatal("page content not escaped")
	}
	var r Report
	data, e = os.ReadFile(filepath.Join(dir, "report.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(data, &r); e != nil {
		t.Fatal(e)
	}
	if r.Glynx.Text != attack {
		t.Fatal("JSON evidence changed")
	}
}
func TestDiffCountsChangedPixelsAndRejectsDifferentSizes(t *testing.T) {
	o := testOptions(t)
	o.Width = 2
	o.Height = 2
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	b := image.NewRGBA(a.Bounds())
	b.Set(1, 1, color.White)
	if e := writePNG(filepath.Join(o.Out, "glynx.png"), a); e != nil {
		t.Fatal(e)
	}
	if e := writePNG(filepath.Join(o.Out, "chrome.png"), b); e != nil {
		t.Fatal(e)
	}
	n, e := writeDiff(o)
	if e != nil || n != 1 {
		t.Fatalf("diff %d %v", n, e)
	}
	o.Width = 3
	if _, e = writeDiff(o); e == nil {
		t.Fatal("accepted unequal viewport")
	}
}
func TestCaptureUsesLoopScriptsAndExactViewport(t *testing.T) {
	for _, scripts := range []bool{false, true} {
		o := testOptions(t)
		o.Scripts = scripts
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r, e := captureGlynx(ctx, o, Source{URL: o.URL, HTML: `<body style="margin:0"><h1 id="title">before</h1><a href="/logo"><img src="data:image/svg+xml,%3Csvg%20xmlns=%22http://www.w3.org/2000/svg%22%20width=%224%22%20height=%224%22%3E%3Crect%20width=%224%22%20height=%224%22%20fill=%22blue%22/%3E%3C/svg%3E"></a><div style="height:2000px;background:#abc">long page</div><script>document.getElementById('title').textContent='after';</script>`})
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if len(r.Images) != 1 || r.Images[0].Status != "LOCATED" || !r.Images[0].Loaded {
			t.Fatalf("anonymous decoded image not attributed: %+v", r.Images)
		}
		want := "before"
		if scripts {
			want = "after"
		}
		if !strings.Contains(r.Text, want) {
			t.Fatalf("scripts %v: %s", scripts, r.Text)
		}
		f, e := os.Open(filepath.Join(o.Out, r.Screenshot))
		if e != nil {
			t.Fatal(e)
		}
		cfg, _, e := image.DecodeConfig(f)
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		if cfg.Width != 1200 || cfg.Height != 900 {
			t.Fatalf("incorrect screenshot dimensions: %+v", cfg)
		}
	}
}
func TestChromeSharedDocument(t *testing.T) {
	if os.Getenv("GLYNX_COMPARE_CHROME_TEST") == "" {
		t.Skip("opt-in local Chrome integration")
	}
	for _, scripts := range []bool{false, true} {
		o := testOptions(t)
		o.Scripts = scripts
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		r, e := captureChrome(ctx, o, Source{URL: o.URL, ContentType: "text/html", Status: 200, HTML: `<link rel="icon" href="data:,"><h1 id="title">shared HTML</h1><a href="/logo"><img src="data:image/svg+xml,%3Csvg%20xmlns=%22http://www.w3.org/2000/svg%22%20width=%224%22%20height=%224%22%3E%3Crect%20width=%224%22%20height=%224%22%20fill=%22blue%22/%3E%3C/svg%3E"></a><script>document.getElementById('title').textContent='script ran';</script>`})
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if len(r.Images) != 1 || !r.Images[0].Loaded || r.Images[0].ID != "" {
			t.Fatalf("Chrome anonymous image missing: %+v", r.Images)
		}
		want := "shared HTML"
		if scripts {
			want = "script ran"
		}
		if !strings.Contains(r.Text, want) || r.Width != o.Width {
			t.Fatalf("wrong document: %+v", r)
		}
	}
}
