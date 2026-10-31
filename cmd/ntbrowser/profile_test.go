package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if p.Name != "locked" || p.Navigate != profile.NavConfirm || p.Subresources != profile.SubOrigin || p.Scripts != profile.ScriptsExternal ||
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
	if !c.ShowProfile || !strings.Contains(out, "profile: reader") || !strings.Contains(out, "free") {
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

func TestSandboxArgsCarryMediaFD(t *testing.T) {
	rest, dir, fd, sandboxed := sandboxArgs([]string{"--sandboxed=/tmp/x", "--media-fd=7", "-profile", "reader", "u"})
	if dir != "/tmp/x" || fd != 7 || !sandboxed || len(rest) != 3 {
		t.Fatalf("%v %q %d %v", rest, dir, fd, sandboxed)
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
