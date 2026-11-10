// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package browsercmd

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
)

// TestPersistFlags: glynx keeps localStorage on and persists state under the
// data directory; --safe (or --persist=off) makes the run memory-only;
// ntbrowser is memory-only with localStorage off.
func TestPersistFlags(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	c, err := parseCommandArgs(Glynx, nil)
	if err != nil || c.Profile.LocalStorage != profile.LocalStorageOn || c.Profile.Persist != profile.PersistFile || c.StateDir != filepath.Join(data, "glynx") {
		t.Fatalf("glynx default: %+v %v", c, err)
	}
	for _, args := range [][]string{{"--safe"}, {"--persist=off"}, {"--persist=file", "--safe"}} {
		c, err = parseCommandArgs(Glynx, args)
		if err != nil || c.Profile.Persist != profile.PersistOff || c.Profile.LocalStorage != profile.LocalStorageOn || c.StateDir != "" {
			t.Fatalf("%v: persist %q dir %q %v", args, c.Profile.Persist, c.StateDir, err)
		}
	}
	c, err = parseCommandArgs(Glynx, []string{"--local-storage=off"})
	if err != nil || c.Profile.LocalStorage != profile.LocalStorageOff || c.Profile.Persist != profile.PersistFile {
		t.Fatalf("local storage off keeps cookie persistence: %+v %v", c.Profile, err)
	}
	c, err = parseCommandArgs(Glynx, []string{"--state-dir", filepath.Join(data, "elsewhere")})
	if err != nil || c.StateDir != filepath.Join(data, "elsewhere") {
		t.Fatalf("explicit dir: %q %v", c.StateDir, err)
	}
	for _, bad := range []string{"--local-storage=file", "--local-storage=memory", "--persist=disk", "--persist=on"} {
		if _, err = parseCommandArgs(Glynx, []string{bad}); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	c, err = parseCommandArgs(NTBrowser, nil)
	if err != nil || c.Profile.LocalStorage != "" || c.Profile.Persist != "" || c.StateDir != "" {
		t.Fatalf("ntbrowser default: %+v %v", c.Profile, err)
	}
	c, err = parseCommandArgs(NTBrowser, []string{"--local-storage=on", "--persist=file", "--cookies=session"})
	if err != nil || c.Profile.Persist != profile.PersistFile || c.StateDir != filepath.Join(data, "ntbrowser") {
		t.Fatalf("ntbrowser opt-in: %+v %v", c, err)
	}
}

// TestStateFilesRoundTrip: both state files are created owner-only, the
// stores load what their sinks wrote, session cookies stay out of the file,
// corrupt content starts empty, and a symlink in a file's place is refused.
func TestStateFilesRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "glynx")
	p, _ := profile.Builtin("glynx")
	st := &state{Config: config{Profile: p, StateDir: dir}}
	local, jar, err := stateFiles(st, false)
	if err != nil || local == nil || jar == nil {
		t.Fatal(err)
	}
	defer local.Close()
	defer jar.Close()
	for _, name := range []string{localStoreName, cookieStoreName} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
	if parent, _ := os.Stat(dir); parent.Mode().Perm() != 0o700 {
		t.Fatalf("parent mode %v", parent.Mode())
	}
	if st.StorageFD < 3 || st.CookieFD < 3 || !filepath.IsAbs(st.Config.LocalStoreFile) || !filepath.IsAbs(st.Config.CookieFile) {
		t.Fatalf("descriptors/paths: %+v", st)
	}

	// localStorage round trip through the sink.
	store, sink := openLocalStore(p, local)
	if store == nil || sink == nil {
		t.Fatal("local store")
	}
	_ = store.Open("https://a.test/").Set("k\x00", "v\x00")
	if err := sink(store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	again, _ := openLocalStore(p, local)
	if v, ok := again.Open("https://a.test/").Get("k\x00"); !ok || v != "v\x00" {
		t.Fatalf("local reload: %q %v", v, ok)
	}

	// Cookies: the persister coalesces writes; only cookies with an expiry
	// reach the file; Close writes the final state.
	cookieStore, saver := openCookieStore(p, jar)
	if cookieStore == nil || saver == nil {
		t.Fatal("cookie store")
	}
	u, _ := url.Parse("https://shop.example.test/")
	cookieStore.Set(u, "sid=abc; Max-Age=3600; Path=/", false)
	cookieStore.Set(u, "temp=1", false)
	saver.Close()
	reloaded, saver2 := openCookieStore(p, jar)
	defer saver2.Close()
	if got := reloaded.Document("https://shop.example.test/x"); got != "sid=abc" {
		t.Fatalf("cookie reload: %q", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, cookieStoreName))
	if !strings.HasPrefix(string(raw), stateMagic) || strings.Contains(string(raw), "temp") {
		t.Fatalf("cookie file: %q", raw)
	}

	// Memory-only runs get stores without sinks or persisters; closed gates get nothing.
	memory := p
	memory.Persist = profile.PersistOff
	if s, sink := openLocalStore(memory, nil); s == nil || sink != nil {
		t.Fatal("memory local store")
	}
	if s, saver := openCookieStore(memory, nil); s == nil || saver != nil {
		t.Fatal("memory cookie store")
	}
	off := p
	off.LocalStorage, off.Cookies = profile.LocalStorageOff, profile.CookiesOff
	if s, _ := openLocalStore(off, nil); s != nil {
		t.Fatal("closed local gate opened a store")
	}
	if s, _ := openCookieStore(off, nil); s != nil {
		t.Fatal("closed cookie gate opened a store")
	}
	if l, j, err := stateFiles(&state{Config: config{Profile: memory}}, false); err != nil || l != nil || j != nil {
		t.Fatal("persist=off opened files")
	}
	if _, _, err := stateFiles(&state{Config: config{Profile: p}}, true); err == nil {
		t.Fatal("sandboxed child without descriptors accepted")
	}

	// Corrupt content starts empty instead of failing the run.
	if err := os.WriteFile(filepath.Join(dir, localStoreName), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g, _ := os.OpenFile(filepath.Join(dir, localStoreName), os.O_RDWR, 0)
	defer g.Close()
	if data, err := loadState(g); err == nil || data != nil {
		t.Fatal("corrupt file loaded")
	}
	empty, _ := openLocalStore(p, g)
	if empty.Open("https://a.test/").Len() != 0 {
		t.Fatal("corrupt file populated the store")
	}

	// A symlink where a state file should be is refused.
	other := filepath.Join(t.TempDir(), "other")
	_ = os.MkdirAll(other, 0o700)
	target := filepath.Join(other, "elsewhere.dat")
	_ = os.WriteFile(target, nil, 0o600)
	if err := os.Symlink(target, filepath.Join(other, localStoreName)); err != nil {
		t.Skip(err)
	}
	if l, _, err := stateFiles(&state{Config: config{Profile: p, StateDir: other}}, false); err == nil {
		l.Close()
		t.Fatal("symlink accepted")
	}
}
