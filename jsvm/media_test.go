// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/dom"
)

func TestMediaQueryEvaluation(t *testing.T) {
	st := MediaState{Width: 1000, Height: 600, ColorScheme: "dark"}
	cases := map[string]bool{
		"(prefers-color-scheme: dark)":                     true,
		"(prefers-color-scheme: light)":                    false,
		"(min-width: 768px)":                               true,
		"(max-width: 767.98px)":                            false,
		"screen and (min-width: 48em)":                     true,
		"only screen and (max-width: 600px)":               false,
		"not all and (max-width: 600px)":                   true,
		"print":                                            false,
		"(width >= 1000px)":                                true,
		"(600px < width <= 1200px)":                        true,
		"(orientation: landscape)":                         true,
		"(hover: hover)":                                   true,
		"(pointer: coarse)":                                false,
		"(prefers-reduced-motion: reduce)":                 false,
		"(prefers-reduced-motion: no-preference)":          true,
		"(-webkit-min-device-pixel-ratio: 2)":              false,
		"(min-resolution: 1dppx)":                          true,
		"(max-width: 600px), (prefers-color-scheme: dark)": true,
		"(color-index: 256)":                               false,
		"garbage":                                          false,
		"":                                                 true,
	}
	for q, want := range cases {
		if got := evalMediaQuery(q, st); got != want {
			t.Errorf("%q = %v, want %v", q, got, want)
		}
	}
}

// matchMedia reflects the host's state live, fires change on a flip through
// both listener styles, and is bounded.
func TestMatchMediaFollowsHostState(t *testing.T) {
	state := MediaState{Width: 400, Height: 300, ColorScheme: "light"}
	tree, _ := dom.Parse(strings.NewReader("<body></body>"), dom.Limits{})
	host := &nopHost{}
	s := New(Config{Media: func() MediaState { return state }}, tree, host)
	t.Cleanup(s.Close)
	err := s.Eval("mq.js", `
		var dark = matchMedia('(prefers-color-scheme: dark)');
		var wide = window.matchMedia('(min-width: 768px)');
		globalThis.log = [dark.matches, wide.matches, dark.media, dark instanceof MediaQueryList, dark instanceof EventTarget];
		dark.addEventListener('change', e => log.push('dark:' + e.matches + ':' + e.media + ':' + (e.target === dark)));
		wide.addListener(e => log.push('wide:' + e.matches));
		dark.onchange = () => log.push('onchange');
	`)
	if err != nil {
		t.Fatal(err)
	}
	state = MediaState{Width: 1024, Height: 300, ColorScheme: "dark"}
	if err := s.MediaChanged(); err != nil {
		t.Fatal(err)
	}
	if err := s.MediaChanged(); err != nil { // no flip, no events
		t.Fatal(err)
	}
	var out string
	if err := s.Eval("check.js", `globalThis.out = log.join('|') + '|' + dark.matches + '|' + wide.matches`); err != nil {
		t.Fatal(err)
	}
	out = s.vm.Get("out").String()
	want := "false|false|(prefers-color-scheme: dark)|true|true|dark:true:(prefers-color-scheme: dark):true|onchange|wide:true|true|true"
	if out != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	if err := s.Eval("many.js", `for (let i = 0; i < 300; i++) try { matchMedia('(min-width: ' + i + 'px)') } catch (e) { globalThis.capped = String(e); break }`); err != nil {
		t.Fatal(err)
	}
	if capped := s.vm.Get("capped"); capped == nil || !strings.Contains(capped.String(), "too many") {
		t.Fatalf("list cap: %v", capped)
	}
}
