// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/nthtml/document"
)

func renderedText(r *document.Result) string {
	var text strings.Builder
	for _, box := range r.Boxes {
		text.WriteString(box.Text)
	}
	return text.String()
}

func TestPopoverPresentationTransitions(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		name := "css"
		if chunked {
			name = "chunked"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			sink := make(chanSink, 256)
			l := New(ctx, Config{Profile: testProfile, Width: 400, MinFrame: time.Millisecond, Chunked: chunked}, sink)
			go l.Run()
			l.LoadPage(`<style>:popover-open{text-transform:uppercase}</style><button id=b type=button popovertarget=p>toggle</button><div id=p popover>secret</div>`, "https://example.test/", nil)
			first := nextFrame(t, sink)
			if strings.Contains(strings.ToLower(renderedText(first.Result)), "secret") {
				t.Fatal("closed popover painted")
			}
			activate := func() { l.post(func() { l.activate(l.page, l.page.Tree.ByID("b")) }) }
			activate()
			opened := frameWhere(t, sink, patience, func(f FrameMsg) bool { return f.Result != nil && strings.Contains(renderedText(f.Result), "SECRET") })
			activate()
			closed := frameWhere(t, sink, patience, func(f FrameMsg) bool {
				return f.Result != nil && !strings.Contains(strings.ToLower(renderedText(f.Result)), "secret")
			})
			if !strings.Contains(renderedText(opened.Result), "SECRET") || strings.Contains(renderedText(closed.Result), "SECRET") {
				t.Fatal("frame state was not immutable")
			}
		})
	}
}

func TestRenderMediaPreferences(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		name := "defaults"
		if restricted {
			name = "restricted"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			sink := make(chanSink, 256)
			cfg := Config{Profile: testProfile, Width: 400, MinFrame: time.Millisecond}
			if restricted {
				cfg.ReducedMotion = "reduce"
				cfg.Hover = "none"
				cfg.Profile.Scripts = "off"
			}
			l := New(ctx, cfg, sink)
			go l.Run()
			l.LoadPage(`<style>.probe{display:none}
@media (prefers-reduced-motion:no-preference){#motion{display:block}}
@media (hover:hover){#hover{display:block}}
@media (scripting:enabled){#script{display:block}}
@supports (display:grid){#supported{display:block}}
@supports (position-area:block-end){#unsupported{display:block}}
</style><p class=probe id=motion>MOTION</p><p class=probe id=hover>HOVER</p><p class=probe id=script>SCRIPT</p><p class=probe id=supported>SUPPORTED</p><p class=probe id=unsupported>UNSUPPORTED</p>`, "https://example.test/", nil)
			f := nextFrame(t, sink)
			text := renderedText(f.Result)
			for _, word := range []string{"MOTION", "HOVER", "SCRIPT"} {
				if strings.Contains(text, word) == restricted {
					t.Errorf("unexpected %s in %q", word, text)
				}
			}
			if !strings.Contains(text, "SUPPORTED") || strings.Contains(text, "UNSUPPORTED") {
				t.Errorf("supports branches: %q", text)
			}
		})
	}
}
