// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package browsercmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sessionstorage"
)

// TestLocalStorageFlags: glynx keeps a file-backed localStorage under the data
// directory, --safe keeps it in memory, --local-storage selects explicitly,
// and ntbrowser stays off.
func TestLocalStorageFlags(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	c, err := parseCommandArgs("glynx", nil)
	if err != nil || c.Profile.LocalStorage != profile.LocalStorageFile || c.LocalStorageFile != filepath.Join(data, "glynx", "local-storage.dat") {
		t.Fatalf("glynx default: %+v %v", c.Profile.LocalStorage, err)
	}
	c, err = parseCommandArgs("glynx", []string{"--safe"})
	if err != nil || c.Profile.LocalStorage != profile.LocalStorageMemory || !c.Safe {
		t.Fatalf("--safe: %q %v", c.Profile.LocalStorage, err)
	}
	c, err = parseCommandArgs("glynx", []string{"--local-storage=off"})
	if err != nil || c.Profile.LocalStorage != profile.LocalStorageOff || c.LocalStorageFile != "" {
		t.Fatalf("off: %q %q %v", c.Profile.LocalStorage, c.LocalStorageFile, err)
	}
	c, err = parseCommandArgs("glynx", []string{"--local-storage-file", filepath.Join(data, "x.dat")})
	if err != nil || c.LocalStorageFile != filepath.Join(data, "x.dat") {
		t.Fatalf("explicit file: %q %v", c.LocalStorageFile, err)
	}
	if _, err = parseCommandArgs("glynx", []string{"--local-storage=disk"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
	c, err = parseCommandArgs("ntbrowser", nil)
	if err != nil || c.Profile.LocalStorage != "" || c.LocalStorageFile != "" {
		t.Fatalf("ntbrowser default: %q %v", c.Profile.LocalStorage, err)
	}
	c, err = parseCommandArgs("ntbrowser", []string{"--local-storage=file", "--safe"})
	if err != nil || c.Profile.LocalStorage != profile.LocalStorageMemory {
		t.Fatalf("--safe over explicit file: %q %v", c.Profile.LocalStorage, err)
	}
}

// TestLocalStoreFileRoundTrip: the store file is created owner-only, a
// snapshot written through the sink loads back, corrupt content starts
// empty, and a symlink in the file's place is refused.
func TestLocalStoreFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "glynx", "local-storage.dat")
	p, _ := profile.Builtin("glynx")
	st := &state{Config: config{Profile: p, LocalStorageFile: path}}
	f, err := localStoreFile(st, false)
	if err != nil || f == nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 || st.StorageFD < 3 || !filepath.IsAbs(st.Config.LocalStorageFile) {
		t.Fatalf("mode %v fd %d path %q", info.Mode(), st.StorageFD, st.Config.LocalStorageFile)
	}
	if parent, _ := os.Stat(filepath.Dir(path)); parent.Mode().Perm() != 0o700 {
		t.Fatalf("parent mode %v", parent.Mode())
	}
	store, sink := openLocalStore(p, f)
	if store == nil || sink == nil || store.Open("https://a.test/").Len() != 0 {
		t.Fatal("empty file should give an empty store with a sink")
	}
	if err := store.Open("https://a.test/").Set("k\x00", "v\x00"); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x\x00", 2000)
	_ = store.Open("https://a.test/").Set("big\x00", big)
	if err := sink(store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	// A smaller rewrite leaves stale bytes behind the header's length; the
	// loader ignores them.
	store.Open("https://a.test/").Remove("big\x00")
	if err := sink(store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := openLocalStore(p, f)
	if v, ok := reloaded.Open("https://a.test/").Get("k\x00"); !ok || v != "v\x00" || reloaded.Open("https://a.test/").Len() != 1 {
		t.Fatalf("reload: %q %v", v, ok)
	}
	// Memory mode never touches a file.
	mem := p
	mem.LocalStorage = profile.LocalStorageMemory
	if s, sink := openLocalStore(mem, nil); s == nil || sink != nil {
		t.Fatal("memory store")
	}
	off := p
	off.LocalStorage = profile.LocalStorageOff
	if s, _ := openLocalStore(off, nil); s != nil {
		t.Fatal("closed gate opened a store")
	}

	// Corrupt content starts empty instead of failing the run.
	if err := os.WriteFile(path, []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g, _ := os.OpenFile(path, os.O_RDWR, 0)
	defer g.Close()
	empty := sessionstorage.New(0, 0)
	if err := loadLocalStore(empty, g); err == nil || empty.Open("https://a.test/").Len() != 0 {
		t.Fatal("corrupt file loaded")
	}

	// A symlink where the store should be is refused.
	target := filepath.Join(dir, "elsewhere.dat")
	_ = os.WriteFile(target, nil, 0o600)
	link := filepath.Join(dir, "link.dat")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	st = &state{Config: config{Profile: p, LocalStorageFile: link}}
	if f, err := localStoreFile(st, false); err == nil {
		f.Close()
		t.Fatal("symlink accepted")
	}
	// The closed gate opens nothing; the sandboxed child needs its descriptor.
	st = &state{Config: config{Profile: off}}
	if f, err := localStoreFile(st, false); err != nil || f != nil {
		t.Fatal("closed gate opened a file")
	}
	st = &state{Config: config{Profile: p}}
	if _, err := localStoreFile(st, true); err == nil {
		t.Fatal("sandboxed child without a descriptor accepted")
	}
}
