// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package browsercmd

import "testing"

func binaryNamed(name string) Binary {
	for _, b := range []Binary{NTBrowser, Glynx} {
		if b.Name == name {
			return b
		}
	}
	panic("no binary named " + name)
}

// A binary's defaults are data: the flags start from them and nothing
// compares the command name.
func TestBinaryDefaultsDriveFlags(t *testing.T) {
	custom := Binary{Name: "custom", Profile: "reader", Browsing: true, HeapBudgetMiB: 128,
		StylesheetBytes: 2048, StylesheetTotalBytes: 4096, Stylesheets: 3, CSSRules: 512,
		ImageBytes: 2048, PageBytes: 128 << 10, Images: 7}
	c, err := parseCommandArgs(custom, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := parseCommandArgs(NTBrowser, []string{"--profile=reader"})
	if c.Profile.Name != want.Profile.Name {
		t.Errorf("profile %q, want %q", c.Profile.Name, want.Profile.Name)
	}
	if c.HeapBudgetMiB != 128 || c.StylesheetBytes != 2048 || c.StylesheetTotalBytes != 4096 || c.Stylesheets != 3 || c.CSSRules != 512 || c.ImageBytes != 2048 || c.PageBytes != 128<<10 || c.Images != 7 {
		t.Errorf("defaults not taken from the binary: %+v", c)
	}
	if c.Start != "https://example.test" && c.Start != "https://example.test/" {
		t.Errorf("a browsing binary left the bare address as %q", c.Start)
	}
	for _, b := range []Binary{NTBrowser, Glynx} {
		if _, err := parseCommandArgs(b, nil); err != nil {
			t.Errorf("%s defaults do not validate: %v", b.Name, err)
		}
	}
}
