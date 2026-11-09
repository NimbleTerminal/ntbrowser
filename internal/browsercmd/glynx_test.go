// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package browsercmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
)

func TestGlynxDefaultsAndOverrides(t *testing.T) {
	c, err := parseCommandArgs("glynx", []string{"https://www.google.com"})
	want, _ := profile.Builtin("glynx")
	if err != nil || !reflect.DeepEqual(c.Profile, want) || c.Start != "https://www.google.com" {
		t.Fatalf("%+v %v", c, err)
	}
	if !want.AllowsDynamicCode() || want.ScriptSources != profile.ScriptSourcesAny || want.Frames != profile.FramesAny || want.FrameDOM != "same-origin" || want.Cookies != profile.CookiesSession || want.SessionStorage != profile.SessionStorageOn || want.Fetch != profile.FetchOrigin || want.PrivateNetwork || want.NativeCode {
		t.Fatal("unexpected browsing authority", want)
	}
	original, err := parseArgs(nil)
	browse, _ := profile.Builtin("browse")
	if err != nil || !reflect.DeepEqual(original.Profile, browse) {
		t.Fatal("ntbrowser default changed", original, err)
	}
	for _, name := range []string{"locked", "reader", "browse"} {
		c, err = parseCommandArgs("glynx", []string{"--profile", name})
		want, _ = profile.Builtin(name)
		if err != nil || !reflect.DeepEqual(c.Profile, want) {
			t.Fatal("default leaked into explicit profile", c, err)
		}
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err = os.WriteFile(path, []byte(`{"scripts":"inline"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = parseCommandArgs("glynx", []string{"--profile-file", path})
	if err != nil || c.Profile.ScriptSources != "" || c.Profile.Frames != "" || c.Profile.Cookies != "" {
		t.Fatal("file inherited glynx authority", c, err)
	}
	c, err = parseCommandArgs("glynx", []string{"--script-sources=grants", "--frames=off", "--cookies=off", "--dynamic-code=off"})
	if err != nil || c.Profile.ScriptSources != profile.ScriptSourcesGrants || c.Profile.Frames != profile.FramesOff || c.Profile.AllowsDynamicCode() || c.Profile.Name != "glynx+flags" {
		t.Fatal(c, err)
	}
	if _, err = parseCommandArgs("glynx", []string{"--script-sources=unknown"}); err == nil {
		t.Fatal("unknown source mode accepted")
	}
}

func TestGlynxStartAndReexec(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		start string
	}{
		{nil, ""}, {[]string{"google.com"}, "https://google.com"},
		{[]string{"hello world"}, "https://www.google.com/search?q=hello+world"},
	} {
		c, err := parseCommandArgs("glynx", tc.args)
		if err != nil || c.Start != tc.start {
			t.Fatal(c, err)
		}
		arg, err := encodeState(state{Config: c})
		if err != nil {
			t.Fatal(err)
		}
		decoded, ok, err := decodeState([]string{arg})
		if err != nil || !ok || !reflect.DeepEqual(decoded.Config, c) {
			t.Fatal("reexec changed policy", decoded, err)
		}
	}
}

func TestDevToolsAndDiagnosticAlias(t *testing.T) {
	for _, command := range []string{"ntbrowser", "glynx"} {
		c, err := parseCommandArgs(command, nil)
		if err != nil || c.Profile.DevTools {
			t.Fatal("devtools enabled by default", c, err)
		}
		c, err = parseCommandArgs(command, []string{"--devtools", "-d", "capture.jsonl"})
		if err != nil || !c.Profile.DevTools || !c.Profile.Diagnostics || c.Diagnostics != "capture.jsonl" {
			t.Fatal(c, err)
		}
		c, err = parseCommandArgs(command, []string{"--devtools=false"})
		if err != nil || c.Profile.DevTools {
			t.Fatal(c, err)
		}
	}
}

func TestDiagnosticShortFlagPreservesDashFilename(t *testing.T) {
	c, err := parseCommandArgs("glynx", []string{"-d", "-devtools"})
	if err != nil || c.Diagnostics != "-devtools" || c.Profile.DevTools {
		t.Fatal(c, err)
	}
}

func TestProcessHeapBudgets(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    uint64
	}{{"ntbrowser", 256}, {"glynx", 512}} {
		c, err := parseCommandArgs(tc.command, nil)
		if err != nil || c.HeapBudgetMiB != tc.want {
			t.Fatalf("%s default: %d, %v", tc.command, c.HeapBudgetMiB, err)
		}
		for _, value := range []string{"64", "768", "2048"} {
			c, err := parseCommandArgs(tc.command, []string{"--heap-budget-mib=" + value})
			if err != nil {
				t.Fatal(err)
			}
			arg, err := encodeState(state{Config: c})
			if err != nil {
				t.Fatal(err)
			}
			decoded, ok, err := decodeState([]string{arg})
			if err != nil || !ok || decoded.Config.HeapBudgetMiB != c.HeapBudgetMiB {
				t.Fatalf("lost heap budget during reexec: %v", err)
			}
		}
		for _, value := range []string{"0", "63", "2049", "-1", "18446744073709551615"} {
			if _, err := parseCommandArgs(tc.command, []string{"--heap-budget-mib=" + value}); err == nil {
				t.Fatalf("accepted budget %s", value)
			}
		}
	}
}

func TestStylesheetBudgets(t *testing.T) {
	for _, command := range []string{"glynx", "ntbrowser"} {
		c, err := parseCommandArgs(command, nil)
		if err != nil {
			t.Fatal(err)
		}
		if command == "glynx" {
			if c.CSSRules != 32768 || c.StylesheetBytes != 1<<20 || c.StylesheetTotalBytes != 4<<20 || c.Stylesheets != 64 {
				t.Fatal(c)
			}
		} else if c.CSSRules != 4096 || c.StylesheetBytes != 256<<10 || c.StylesheetTotalBytes != 0 || c.Stylesheets != 16 {
			t.Fatal(c)
		}
		c, err = parseCommandArgs(command, []string{"--stylesheet-bytes=524288", "--stylesheet-total-bytes=2097152", "--stylesheets=32", "--css-rules=8192"})
		if err != nil {
			t.Fatal(err)
		}
		arg, _ := encodeState(state{Config: c})
		got, ok, err := decodeState([]string{arg})
		if err != nil || !ok || !reflect.DeepEqual(c, got.Config) {
			t.Fatal("reexec lost budgets", err)
		}
	}
	for _, arg := range []string{"--css-rules=0", "--css-rules=32769", "--stylesheet-bytes=0", "--stylesheet-bytes=1048577", "--stylesheet-total-bytes=-1", "--stylesheet-total-bytes=4194305", "--stylesheets=0", "--stylesheets=65"} {
		if _, err := parseArgs([]string{arg}); err == nil {
			t.Fatalf("accepted %s", arg)
		}
	}
}

// --image-bytes raises the per-image download cap for README demo GIFs;
// glynx defaults to 8 MiB, ntbrowser keeps 1 MiB, and the ceiling is 16 MiB.
func TestImageBudget(t *testing.T) {
	for _, command := range []string{"glynx", "ntbrowser"} {
		c, err := parseCommandArgs(command, nil)
		if err != nil {
			t.Fatal(command, err)
		}
		want := 1 << 20
		if command == "glynx" {
			want = 8 << 20
		}
		if c.ImageBytes != want {
			t.Fatalf("%s default image bytes %d, want %d", command, c.ImageBytes, want)
		}
		c, err = parseCommandArgs(command, []string{"--image-bytes=16777216"})
		if err != nil || c.ImageBytes != 16<<20 {
			t.Fatalf("%s override: %d %v", command, c.ImageBytes, err)
		}
		for _, arg := range []string{"--image-bytes=0", "--image-bytes=1023", "--image-bytes=16777217"} {
			if _, err := parseCommandArgs(command, []string{arg}); err == nil {
				t.Fatalf("%s accepted %s", command, arg)
			}
		}
	}
}

// --color-scheme defaults to following the terminal and otherwise takes
// light or dark; anything else is refused.
func TestColorSchemeFlag(t *testing.T) {
	c, err := parseCommandArgs("glynx", []string{"https://example.test/"})
	if err != nil || c.ColorScheme != "auto" {
		t.Fatalf("default scheme %q, %v", c.ColorScheme, err)
	}
	c, err = parseCommandArgs("glynx", []string{"--color-scheme=dark", "https://example.test/"})
	if err != nil || c.ColorScheme != "dark" || startScheme(c.ColorScheme) != "dark" {
		t.Fatalf("dark: %q %v", c.ColorScheme, err)
	}
	if startScheme("auto") != "light" {
		t.Fatal("auto must start light until the terminal answers")
	}
	if _, err := parseCommandArgs("glynx", []string{"--color-scheme=sepia", "https://example.test/"}); err == nil {
		t.Fatal("sepia accepted")
	}
}

// --page-bytes bounds a document's HTML and DOM: 4 MiB in glynx, 1 MiB in
// ntbrowser, 64 KiB to 8 MiB when set.
func TestPageBudget(t *testing.T) {
	for _, command := range []string{"glynx", "ntbrowser"} {
		c, err := parseCommandArgs(command, nil)
		if err != nil {
			t.Fatal(command, err)
		}
		want := 1 << 20
		if command == "glynx" {
			want = 4 << 20
		}
		if c.PageBytes != want {
			t.Fatalf("%s default page bytes %d, want %d", command, c.PageBytes, want)
		}
		c, err = parseCommandArgs(command, []string{"--page-bytes=8388608"})
		if err != nil || c.PageBytes != 8<<20 {
			t.Fatalf("%s override: %d %v", command, c.PageBytes, err)
		}
		for _, bad := range []string{"--page-bytes=1024", "--page-bytes=8388609"} {
			if _, err := parseCommandArgs(command, []string{bad}); err == nil {
				t.Fatalf("%s accepted %s", command, bad)
			}
		}
	}
}

// --images bounds distinct images per page: 256 in glynx, 64 in ntbrowser,
// 1 to 1024 when set; the pixel budget still bounds decoded memory.
func TestImagesFlag(t *testing.T) {
	for _, command := range []string{"glynx", "ntbrowser"} {
		c, err := parseCommandArgs(command, nil)
		if err != nil {
			t.Fatal(command, err)
		}
		want := 64
		if command == "glynx" {
			want = 256
		}
		if c.Images != want {
			t.Fatalf("%s default images %d, want %d", command, c.Images, want)
		}
		if c, err := parseCommandArgs(command, []string{"--images=1024"}); err != nil || c.Images != 1024 {
			t.Fatalf("%s override: %d %v", command, c.Images, err)
		}
		if _, err := parseCommandArgs(command, []string{"--images=1025"}); err == nil {
			t.Fatalf("%s accepted 1025 images", command)
		}
	}
}
