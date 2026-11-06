// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
)

func TestBrowseIsTodaysDefaults(t *testing.T) {
	c, err := parseArgs([]string{"https://a.test/"})
	if err != nil {
		t.Fatal(err)
	}
	p := c.Profile
	if p.Name != "browse" || p.Navigate != profile.NavFree || p.Subresources != profile.SubAny || p.Scripts != profile.ScriptsInline ||
		p.Fetch != profile.FetchAllowlist || len(p.Hosts) != 0 || p.PrivateNetwork || p.Files != profile.FilesStartPage || p.ClockMillis != 10 {
		t.Fatalf("%+v", p)
	}
	if c.Width != 800 || c.Start != "https://a.test/" {
		t.Fatalf("%+v", c)
	}
}

func TestFlagsOverrideProfile(t *testing.T) {
	c, err := parseArgs([]string{"-profile", "locked", "-nav", "confirm", "-subresources", "origin", "-external-scripts", "-allow", "a.test,.b.test", "-allow-private", "-fetch", "origin", "-clipboard", "copy", "https://a.test/"})
	if err != nil {
		t.Fatal(err)
	}
	p := c.Profile
	if p.Name != "locked+flags" || p.Navigate != profile.NavConfirm || p.Subresources != profile.SubOrigin || p.Scripts != profile.ScriptsExternal ||
		len(p.Hosts) != 2 || !p.PrivateNetwork || p.Fetch != profile.FetchOrigin || p.Clipboard != profile.ClipboardCopy {
		t.Fatalf("%+v", p)
	}
}

func TestMediaVideoFlagOpensItsGates(t *testing.T) {
	c, err := parseArgs([]string{"-media", "video", "https://a.test/"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Profile.Media != profile.MediaVideo || !c.Profile.NativeCode || c.Profile.Files != profile.FilesTemp {
		t.Fatalf("%+v", c.Profile)
	}
}

func TestProfileFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(path, []byte(`{"name":"mine","base":"reader","media":"video"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseArgs([]string{"-profile-file", path, "https://a.test/"}); err == nil || !strings.Contains(err.Error(), "native-code") {
		t.Fatalf("file gets no shorthand: %v", err)
	}
	os.WriteFile(path, []byte(`{"name":"mine","base":"reader","scripts":"inline"}`), 0o600)
	c, err := parseArgs([]string{"-profile-file", path, "https://a.test/"})
	if err != nil || c.Profile.Name != "mine" || c.Profile.Scripts != profile.ScriptsInline {
		t.Fatalf("%+v %v", c.Profile, err)
	}
}

func TestLockedFileStartRefused(t *testing.T) {
	if _, err := parseArgs([]string{"-profile", "locked", "file:///x.html"}); err == nil || !strings.Contains(err.Error(), "files") {
		t.Fatalf("%v", err)
	}
}

func TestShowProfilePrintsEffective(t *testing.T) {
	c, err := parseArgs([]string{"-show-profile", "-profile", "reader", "-nav", "free"})
	if err != nil {
		t.Fatal(err)
	}
	out := c.Profile.Table(nil)
	if !c.ShowProfile || !strings.Contains(out, "profile: reader+flags") || !strings.Contains(out, "free") {
		t.Fatalf("%s", out)
	}
}

func TestUnknownProfileFails(t *testing.T) {
	if _, err := parseArgs([]string{"-profile", "nope", "https://a.test/"}); err == nil {
		t.Fatal("accepted")
	}
}

func TestHelperOnlyWhenVideo(t *testing.T) {
	p, _ := profile.Builtin("browse")
	if needsHelper(p) {
		t.Fatal("browse must not start the helper")
	}
	if !needsHelper(p.WithVideo()) {
		t.Fatal("video needs the helper")
	}
}

// All native code lives in cmd/ntbrowser-media: a compromised page must never
// share a process with it.
func TestMainDoesNotLinkNativeCode(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, bad := range []string{"ntvideo", "goffi"} {
		if strings.Contains(string(out), bad) {
			t.Fatalf("ntbrowser links %s", bad)
		}
	}
}

// Sandbox policies match real paths; a symlinked temp area must be resolved.
func TestMediaTempIsResolved(t *testing.T) {
	t.Setenv("TMPDIR", symlinkedDir(t))
	dir, err := mediaTemp()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil || real != dir {
		t.Fatalf("%q resolves to %q (%v)", dir, real, err)
	}
}

func symlinkedDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestStateSurvivesReexec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(path, []byte(`{"name":"mine","base":"reader"}`), 0o600)
	c, err := parseArgs([]string{"-profile-file", path, "-width", "640", "-chunked", "https://a.test/"})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(path) // the sandboxed run never reads inputs again
	arg, err := encodeState(state{Config: c, TempDir: "/t", HelperFD: 7, HelperPID: 99})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := decodeState([]string{arg})
	if err != nil || !ok || !got.Config.Chunked || got.Config.Profile.Name != "mine" || got.Config.Width != 640 || got.Config.Start != "https://a.test/" || got.TempDir != "/t" || got.HelperFD != 7 || got.HelperPID != 99 {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if _, ok, _ := decodeState([]string{"-profile", "x"}); ok {
		t.Fatal("ordinary arguments read as state")
	}
}

func TestFlagsChangeTheName(t *testing.T) {
	c, err := parseArgs([]string{"-profile", "locked", "-scripts", "inline", "https://a.test/"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Profile.Name != "locked+flags" {
		t.Fatalf("%q", c.Profile.Name)
	}
	c, err = parseArgs([]string{"-profile", "locked", "-scripts", "off", "https://a.test/"})
	if err != nil || c.Profile.Name != "locked" {
		t.Fatalf("a flag that changes nothing: %q %v", c.Profile.Name, err)
	}
}

func TestStartPathResolved(t *testing.T) {
	real := filepath.Join(t.TempDir(), "x.html")
	os.WriteFile(real, []byte("x"), 0o600)
	want, _ := filepath.EvalSymlinks(real)
	link := filepath.Join(t.TempDir(), "link.html")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if got := startPath("file://" + link); got != want {
		t.Fatalf("%q, want %q", got, want)
	}
	if got := startPath("https://a.test/"); got != "" {
		t.Fatalf("%q", got)
	}
}

func TestChunkedModeIsOptIn(t *testing.T) {
	defaultConfig, err := parseArgs([]string{"https://example.test/"})
	if err != nil || defaultConfig.Chunked {
		t.Fatalf("default renderer changed: %+v %v", defaultConfig, err)
	}
	chunked, err := parseArgs([]string{"-chunked", "https://example.test/"})
	if err != nil || !chunked.Chunked {
		t.Fatalf("chunk flag missing: %+v %v", chunked, err)
	}
	if !reflect.DeepEqual(defaultConfig.Profile, chunked.Profile) {
		t.Fatal("rendering flag changed security gates")
	}
}

func TestCookieFlag(t *testing.T) {
	c, err := parseArgs([]string{"https://example.test/", "--cookies=session"})
	if err != nil || c.Profile.Cookies != profile.CookiesSession || c.Profile.Name != "browse+flags" {
		t.Fatal(c, err)
	}
	if _, err := parseArgs([]string{"--cookies=all", "https://example.test/"}); err == nil {
		t.Fatal("accepted unknown policy")
	}
}

func TestScriptNavigationFlags(t *testing.T) {
	c, err := parseArgs([]string{"https://example.test/", "--script-navigation=origin", "--script-navigation-limit=3"})
	if err != nil || c.Profile.ScriptNavigation != profile.ScriptNavOrigin || c.Profile.ScriptNavBudget() != 3 {
		t.Fatal(c, err)
	}
	if _, err := parseArgs([]string{"--script-navigation-limit=101", "https://example.test/"}); err == nil {
		t.Fatal("unbounded navigation budget")
	}
}

func TestFrameResourceFlags(t *testing.T) {
	c, err := parseArgs([]string{"--frame-ms=16", "--frame-callbacks=32", "--frame-budget-ms=4", "https://example.test/"})
	if err != nil || c.Profile.FrameInterval() != 16 || c.Profile.FrameCallbackLimit() != 32 || c.Profile.FrameBudget() != 4 {
		t.Fatal(c, err)
	}
}
