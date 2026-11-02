// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package profile

import (
	"reflect"
	"strings"
	"testing"
)

func TestZeroProfileAllClosed(t *testing.T) {
	var p Profile
	if p.NavigationOpen() || p.ScriptsOn() || p.Fetch == FetchAllowlist || p.Fetch == FetchOrigin ||
		p.AllowsSubresources() || p.NativeCode || p.PrivateNetwork || p.ReadsStartFile() || p.UsesTemp() ||
		p.Clipboard == ClipboardCopy || p.Media.Allows(MediaImage) {
		t.Fatalf("zero profile opens something: %+v", p)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinsMatchDesign(t *testing.T) {
	want := map[string]Profile{
		"locked": {Name: "locked", Navigate: NavOff, Subresources: SubOff, Fetch: FetchOff, Scripts: ScriptsOff, Files: FilesOff, Media: MediaOff, Clipboard: ClipboardOff, ClockMillis: 10, MaxMediaBytes: DefaultMaxMediaBytes, Enforcement: EnforceRequired},
		"reader": {Name: "reader", Navigate: NavConfirm, Subresources: SubOrigin, Fetch: FetchOff, Scripts: ScriptsOff, Files: FilesOff, Media: MediaImage, Clipboard: ClipboardCopy, ClockMillis: 10, MaxMediaBytes: DefaultMaxMediaBytes, Enforcement: EnforceBestEffort},
		"browse": {Name: "browse", Navigate: NavFree, Subresources: SubAny, Fetch: FetchAllowlist, Scripts: ScriptsInline, Files: FilesStartPage, Media: MediaAnimation, Clipboard: ClipboardCopy, ClockMillis: 10, MaxMediaBytes: DefaultMaxMediaBytes, Enforcement: EnforceBestEffort},
	}
	for name, w := range want {
		got, ok := Builtin(name)
		if !ok || !reflect.DeepEqual(got, w) {
			t.Errorf("%s:\n got %+v\nwant %+v", name, got, w)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, ok := Builtin("nope"); ok {
		t.Error("unknown built-in")
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	if _, err := Parse([]byte(`{"navigate":"free","surprise":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Parse([]byte(`{"navigate":"sometimes"}`)); err == nil || !strings.Contains(err.Error(), "navigate") {
		t.Fatalf("bad value: %v", err)
	}
}

func TestParseBase(t *testing.T) {
	p, err := Parse([]byte(`{"name":"mine","base":"reader","scripts":"inline","hosts":["a.test"],"max-media-bytes":1000}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "mine" || p.Navigate != NavConfirm || p.Scripts != ScriptsInline || p.Hosts[0] != "a.test" || p.MaxMediaBytes != 1000 {
		t.Fatalf("%+v", p)
	}
	if p, err = Parse([]byte(`{"name":"none"}`)); err != nil || p.NavigationOpen() || p.ScriptsOn() {
		t.Fatalf("no base must start closed: %+v %v", p, err)
	}
	if _, err = Parse([]byte(`{"base":"nope"}`)); err == nil {
		t.Fatal("unknown base accepted")
	}
}

func TestValidateVideoNeedsGates(t *testing.T) {
	p, _ := Builtin("browse")
	p.Media = MediaVideo
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "native-code") {
		t.Fatalf("%v", err)
	}
	p.NativeCode = true
	if err = p.Validate(); err == nil || !strings.Contains(err.Error(), "files") {
		t.Fatalf("%v", err)
	}
	p.Files = FilesTemp
	if err = p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateStartFileUnderFilesOff(t *testing.T) {
	locked, _ := Builtin("locked")
	if err := locked.ValidateStart("file:///x.html"); err == nil || !strings.Contains(err.Error(), "files") {
		t.Fatalf("%v", err)
	}
	if err := locked.ValidateStart("https://a.test/"); err != nil {
		t.Fatal(err)
	}
	browse, _ := Builtin("browse")
	if err := browse.ValidateStart("file:///x.html"); err != nil {
		t.Fatal(err)
	}
	if err := browse.ValidateStart("ftp://a.test/"); err == nil {
		t.Fatal("ftp accepted")
	}
}

func TestWithVideoSetsThree(t *testing.T) {
	p, _ := Builtin("browse")
	p = p.WithVideo()
	if p.Media != MediaVideo || !p.NativeCode || p.Files != FilesTemp {
		t.Fatalf("%+v", p)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if !p.ReadsStartFile() {
		t.Fatal("files=temp includes the start page")
	}
}

func TestMediaIsCumulative(t *testing.T) {
	if !MediaVideo.Allows(MediaAnimation) || !MediaAnimation.Allows(MediaImage) || MediaImage.Allows(MediaAnimation) || MediaOff.Allows(MediaImage) {
		t.Fatal("media order")
	}
}

func TestTableIsReadable(t *testing.T) {
	p, _ := Builtin("locked")
	out := p.Table(Strengths{"files": StrengthOS, "process": StrengthOS})
	for _, want := range []string{"locked", "navigate", "subresources", "fetch", "hosts", "private-network", "scripts", "clock", "files", "native-code", "media", "max-media-bytes", "clipboard", "environment", "process", "enforcement", "embedded images: subresources", "os"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

func TestTableNamesTheHelper(t *testing.T) {
	p, _ := Builtin("browse")
	if strings.Contains(p.Table(nil), "media-helper") {
		t.Fatal("no helper without video")
	}
	if out := p.WithVideo().Table(nil); !strings.Contains(out, "media-helper") || !strings.Contains(out, "started before lockdown") {
		t.Fatalf("%s", out)
	}
}
