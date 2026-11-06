// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/pflag"
)

func TestGNUFlagsAndDynamicGate(t *testing.T) {
	for _, args := range [][]string{
		{"--dynamic-code=on", "https://example.test/"},
		{"https://example.test/", "--dynamic-code", "on"},
		{"-dynamic-code", "on", "https://example.test/"},
	} {
		c, err := parseArgs(args)
		if err != nil || !c.Profile.AllowsDynamicCode() || c.Profile.Name != "browse+flags" || c.Start != "https://example.test/" {
			t.Fatalf("%v: %+v %v", args, c, err)
		}
	}
	for _, help := range []string{"--help", "-h", "-help"} {
		if _, err := parseArgs([]string{help}); !errors.Is(err, pflag.ErrHelp) {
			t.Fatalf("%s: %v", help, err)
		}
	}
	if _, err := parseArgs([]string{"--dynamic-code=maybe", "https://example.test/"}); err == nil {
		t.Fatal("accepted unknown gate value")
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(`{"base":"browse","dynamic-code":"on"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, off := range []bool{false, true} {
		args := []string{"--profile-file", path, "https://example.test/"}
		if off {
			args = append(args, "--dynamic-code=off")
		}
		c, err := parseArgs(args)
		if err != nil || c.Profile.AllowsDynamicCode() == off {
			t.Fatalf("%+v %v", c, err)
		}
	}
}

func TestLegacyFlagsPreserveValuesAndSeparator(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("profile", "", "")
	fs.Bool("show-profile", false, "")
	input := []string{"-profile", "-show-profile", "-show-profile", "--", "-profile"}
	want := []string{"--profile", "-show-profile", "--show-profile", "--", "-profile"}
	if got := legacyLongFlags(fs, input); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if input[0] != "-profile" {
		t.Fatal("mutated caller arguments")
	}
}

func TestHelpExitsSuccessfully(t *testing.T) {
	previous := os.Args
	t.Cleanup(func() { os.Args = previous })
	for _, help := range []string{"--help", "-h", "-help"} {
		os.Args = []string{"ntbrowser", help}
		if code := runMain(); code != 0 {
			t.Fatalf("%s: exit %d", help, code)
		}
	}
}
