// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package main

import (
	"strings"
	"testing"

	"nimbleterminal.dev/ntmediabox"
)

func TestDecoderRefusesPathNames(t *testing.T) {
	d := newVideoDecoder(t.TempDir())
	defer d.Close()
	for _, name := range []string{"", ".", "..", "../x", "/etc/passwd", "a/b", `a\b`, "-option", "file:clip", "a\x00b"} {
		if err := d.Load(ntmediabox.OpenRequest{Name: name}); err == nil || !strings.Contains(err.Error(), "bad media name") {
			t.Errorf("Load(%q) = %v", name, err)
		}
	}
}

func TestDecoderSupportsUnloadBeforeLoad(t *testing.T) {
	d := newVideoDecoder(t.TempDir())
	defer d.Close()
	u, ok := d.(ntmediabox.Unloader)
	if !ok {
		t.Fatal("shared decoder must release media on Stop")
	}
	if err := u.Unload(); err != nil {
		t.Fatal(err)
	}
	s, err := d.Status()
	if err != nil || s.State != ntmediabox.Idle {
		t.Fatalf("status after unload: %+v, %v", s, err)
	}
}
