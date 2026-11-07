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
