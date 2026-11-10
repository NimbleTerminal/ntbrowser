// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

//go:build unix

package browsercmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"nimbleterminal.dev/ntbrowser/cookies"
	"nimbleterminal.dev/ntbrowser/profile"
)

// dupFD returns a second descriptor for f, as the re-executed process would
// inherit one; the caller owns it through the *os.File it is handed to.
func dupFD(t *testing.T, f *os.File) int {
	t.Helper()
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

func tempFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "state")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// Inside the sandbox the stores come only from inherited descriptors: a
// missing one is an error, never a reason to open a path.
func TestSandboxedStateFilesComeFromDescriptorsOnly(t *testing.T) {
	p, _ := profile.Builtin("glynx")
	dir := filepath.Join(t.TempDir(), "never-created")
	st := &state{Config: config{Profile: p, StateDir: dir}}
	if _, _, err := stateFiles(st, true); err == nil || !strings.Contains(err.Error(), "local-storage descriptor") {
		t.Fatalf("no storage descriptor: %v", err)
	}
	st.StorageFD = dupFD(t, tempFile(t))
	local, _, err := stateFiles(st, true)
	if err == nil || !strings.Contains(err.Error(), "cookies descriptor") {
		t.Fatalf("no cookie descriptor: %v", err)
	}
	if local != nil {
		t.Fatal("a failed open still returned a file")
	}
	if err := syscall.Close(st.StorageFD); err == nil {
		t.Fatal("the storage descriptor was left open after the failure")
	}

	st.StorageFD, st.CookieFD = dupFD(t, tempFile(t)), dupFD(t, tempFile(t))
	local, jar, err := stateFiles(st, true)
	if err != nil || local == nil || jar == nil {
		t.Fatalf("inherited descriptors: %v", err)
	}
	if int(local.Fd()) != st.StorageFD || int(jar.Fd()) != st.CookieFD {
		t.Fatal("the stores are not the inherited descriptors")
	}
	local.Close()
	jar.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the sandboxed process touched the state directory")
	}

	// Only the gates that are open ask for a descriptor.
	p.LocalStorage, p.Cookies = profile.LocalStorageOff, profile.CookiesOff
	if local, jar, err := stateFiles(&state{Config: config{Profile: p}}, true); err != nil || local != nil || jar != nil {
		t.Fatalf("closed gates: %v %v %v", local, jar, err)
	}
}

func TestStateFilesNeedPersistAndADirectory(t *testing.T) {
	p, _ := profile.Builtin("glynx")
	off := p
	off.Persist = profile.PersistOff
	dir := filepath.Join(t.TempDir(), "unused")
	if local, jar, err := stateFiles(&state{Config: config{Profile: off, StateDir: dir}}, false); err != nil || local != nil || jar != nil {
		t.Fatalf("persist=off opened files: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("persist=off created the state directory")
	}
	if _, _, err := stateFiles(&state{Config: config{Profile: p}}, false); err == nil {
		t.Fatal("persist=file without a directory was accepted")
	}
	blocked := tempFile(t).Name() // a file where the directory should be
	if _, _, err := stateFiles(&state{Config: config{Profile: p, StateDir: blocked}}, false); err == nil {
		t.Fatal("a file was accepted as the state directory")
	}
}

// A state file must be this user's regular file: loose modes are tightened,
// and a directory or symlink in its place is refused.
func TestOpenStateFileOwnerOnlyRegularFile(t *testing.T) {
	dir := t.TempDir()
	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(loose, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f, resolved, err := openStateFile(loose)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if info, _ := os.Stat(loose); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode left at %v", info.Mode().Perm())
	}
	if want, _ := filepath.EvalSymlinks(loose); resolved != want {
		t.Fatalf("resolved %q, want %q", resolved, want)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if f, _, err := openStateFile(sub); err == nil {
		f.Close()
		t.Fatal("a directory was opened as a state file")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(loose, link); err != nil {
		t.Fatal(err)
	}
	if f, _, err := openStateFile(link); err == nil {
		f.Close()
		t.Fatal("a symlink was followed")
	}
	if f, _, err := openStateFile(filepath.Join(dir, "missing", "file")); err == nil {
		f.Close()
		t.Fatal("a file in a missing directory was opened")
	}
}

func TestSandboxedDiagnosticsComeFromDescriptorOnly(t *testing.T) {
	p, _ := profile.Builtin("browse")
	p.Diagnostics = true
	path := filepath.Join(t.TempDir(), "never-created.jsonl")
	st := &state{Config: config{Profile: p, Diagnostics: path}}
	if f, err := diagnosticFile(st, true); err == nil || f != nil {
		t.Fatalf("no descriptor: %v %v", f, err)
	}
	st.DiagnosticFD = dupFD(t, tempFile(t))
	f, err := diagnosticFile(st, true)
	if err != nil || int(f.Fd()) != st.DiagnosticFD {
		t.Fatalf("inherited descriptor: %v", err)
	}
	f.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the sandboxed process opened the diagnostics path")
	}
}

// Many cookie changes are one write, and a failing disk is remembered so it
// is reported once while the session carries on.
func TestCookiePersisterCoalescesAndSurvivesFailure(t *testing.T) {
	var writes atomic.Int32
	cp := &cookiePersister{store: cookies.New(), sink: func([]byte) error {
		writes.Add(1)
		return errors.New("disk full")
	}}
	for range 5 {
		cp.changed()
	}
	cp.flush()
	cp.flush() // nothing new: no write
	if n := writes.Load(); n != 1 {
		t.Fatalf("5 changes made %d writes, want 1", n)
	}
	if !cp.fail {
		t.Fatal("the failure was not recorded")
	}
	cp.Close() // the final write is unconditional
	if n := writes.Load(); n != 2 {
		t.Fatalf("Close made %d writes in total, want 2", n)
	}
	(*cookiePersister)(nil).Close()
}

// The exits Run takes before it touches the terminal or the sandbox.
func TestRunEarlyExits(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	args, stdout, stderr := os.Args, os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	t.Cleanup(func() {
		os.Args, os.Stdout, os.Stderr = args, stdout, stderr
		devnull.Close()
	})
	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"--show-profile"}, 0},
		{[]string{"--profile=locked", "--show-profile"}, 0},
		{[]string{"--no-such-flag"}, 2},
		{[]string{"--profile=nope"}, 2},
		{[]string{"a.test", "b.test"}, 2},
		{[]string{"--sandboxed", "not-state"}, 2},
	} {
		for _, b := range []Binary{NTBrowser, Glynx} {
			os.Args = append([]string{b.Name}, c.args...)
			if got := Run(b); got != c.want {
				t.Errorf("%s %v: exit %d, want %d", b.Name, c.args, got, c.want)
			}
		}
	}
}
