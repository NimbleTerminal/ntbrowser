package main

import (
	"os"
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
