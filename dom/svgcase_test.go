// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package dom

import (
	"context"
	"strings"
	"testing"

	"nimbleterminal.dev/nthtml/document"
)

const gradientSVG = `<svg width="40" height="20" viewBox="0 0 40 20" preserveAspectRatio="none"><defs><linearGradient id="g" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs><rect width="40" height="20" fill="url(#g)"/></svg>`

// Parsed SVG keeps the spec's mixed-case element and attribute names
// (viewBox, linearGradient, ...); nthtml reads SVG case-sensitively.
func TestSerializeKeepsSVGCase(t *testing.T) {
	out := mustParse(t, `<body>`+gradientSVG+`</body>`, Limits{}).Serialize()
	for _, want := range []string{`viewBox="0 0 40 20"`, `preserveAspectRatio="none"`, `<linearGradient id="g" gradientUnits="userSpaceOnUse">`, `</linearGradient>`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	for _, bad := range []string{"viewbox", "lineargradient", "gradientunits", "preserveaspectratio"} {
		if strings.Contains(out, bad) {
			t.Fatalf("lowercased %q in\n%s", bad, out)
		}
	}
}

// Elements and attributes created by scripts inside an <svg> serialize with
// the SVG spelling too (createElement lowercases, as in an HTML document).
func TestSerializeSVGCaseForScriptBuiltNodes(t *testing.T) {
	tr := mustParse(t, `<body><svg id="s" width="10" height="10"></svg></body>`, Limits{})
	svg := tr.ByID("s")
	g, err := tr.NewElement("linearGradient")
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Append(svg, g); err != nil {
		t.Fatal(err)
	}
	if err := tr.SetAttr(svg, "viewBox", "0 0 10 10"); err != nil {
		t.Fatal(err)
	}
	if err := tr.SetAttr(g, "GRADIENTUNITS", "objectBoundingBox"); err != nil {
		t.Fatal(err)
	}
	out := tr.Serialize()
	if !strings.Contains(out, `viewBox="0 0 10 10"`) || !strings.Contains(out, `<linearGradient gradientUnits="objectBoundingBox"></linearGradient>`) {
		t.Fatalf("script-built SVG lost its case:\n%s", out)
	}
	if svg.Tag() != "svg" || g.Tag() != "lineargradient" {
		t.Fatalf("matching names must stay lowercase: %q %q", svg.Tag(), g.Tag())
	}
	if v, ok := svg.Attr("viewBox"); !ok || v != "0 0 10 10" {
		t.Fatal("attribute lookup must stay case-insensitive")
	}
}

// Outside <svg>, and inside <foreignObject> (HTML again), names stay lowercase.
func TestSVGCaseOnlyInsideSVG(t *testing.T) {
	out := mustParse(t, `<body><div viewbox="1"><lineargradient></lineargradient></div><svg width="10" height="10"><foreignObject width="10" height="10"><div viewbox="2">x</div></foreignObject></svg></body>`, Limits{}).Serialize()
	for _, want := range []string{`<div viewbox="1"><lineargradient></lineargradient></div>`, `<foreignObject width="10" height="10"><div viewbox="2">x</div></foreignObject>`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
}

// End to end: an inline SVG gradient renders through nthtml without the
// "unsupported SVG element lineargradient" fallback, and the byte bound holds.
func TestSVGGradientRendersAfterRoundTrip(t *testing.T) {
	tr := mustParse(t, `<body>`+gradientSVG+`</body>`, Limits{})
	out := tr.Serialize()
	if len(out) > tr.Bytes() {
		t.Fatalf("serialized %d > accounted %d", len(out), tr.Bytes())
	}
	res, err := document.Render(context.Background(), strings.NewReader(out), document.Options{Width: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("nthtml warnings: %v", res.Warnings)
	}
}
