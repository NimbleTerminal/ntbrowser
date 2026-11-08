// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package browsercmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"nimbleterminal.dev/ntbrowser/cookies"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sessionstorage"
)

// Persistent state (persist=file) lives in two owner-only files under the
// state directory: local-storage.dat and cookies.dat. Each is a header line
// with the payload length followed by the store's snapshot, rewritten in
// place from offset 0 so no truncation is needed; stale bytes past the
// payload are ignored. Both are opened before lockdown, like the
// diagnostics file, and their inherited descriptors are the only write
// paths the sandboxed process keeps.
const (
	stateMagic       = "glynx-state v1 "
	localStoreName   = "local-storage.dat"
	cookieStoreName  = "cookies.dat"
	stateFlushDelay  = time.Second
	maxStatePayload  = sessionstorage.MaxSnapshotBytes
	stateHeaderLimit = len(stateMagic) + 32
)

// defaultStateDir is $XDG_DATA_HOME/<command>, or the platform's data
// directory when XDG_DATA_HOME is unset.
func defaultStateDir(command string) (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if runtime.GOOS == "darwin" {
			base = filepath.Join(home, "Library", "Application Support")
		} else {
			base = filepath.Join(home, ".local", "share")
		}
	}
	return filepath.Join(base, command), nil
}

// stateFiles opens the persistent stores the profile uses, only before
// lockdown: the localStorage file when local-storage is on and the cookie
// file when cookies are on, both under persist=file. The sandboxed child
// receives the descriptors through state instead of paths.
func stateFiles(st *state, sandboxed bool) (local, jar *os.File, err error) {
	p := st.Config.Profile
	if p.Persist != profile.PersistFile {
		return nil, nil, nil
	}
	if sandboxed {
		if p.LocalStorage == profile.LocalStorageOn {
			if local, err = inheritedFile(st.StorageFD, "local-storage"); err != nil {
				return nil, nil, err
			}
		}
		if p.Cookies == profile.CookiesSession {
			if jar, err = inheritedFile(st.CookieFD, "cookies"); err != nil {
				return nil, nil, err
			}
		}
		return local, jar, nil
	}
	if st.Config.StateDir == "" {
		return nil, nil, errors.New("persist=file needs --state-dir")
	}
	if err := os.MkdirAll(st.Config.StateDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("state directory: %w", err)
	}
	if p.LocalStorage == profile.LocalStorageOn {
		if local, st.Config.LocalStoreFile, err = openStateFile(filepath.Join(st.Config.StateDir, localStoreName)); err != nil {
			return nil, nil, err
		}
		st.StorageFD = int(local.Fd())
	}
	if p.Cookies == profile.CookiesSession {
		if jar, st.Config.CookieFile, err = openStateFile(filepath.Join(st.Config.StateDir, cookieStoreName)); err != nil {
			if local != nil {
				local.Close()
			}
			return nil, nil, err
		}
		st.CookieFD = int(jar.Fd())
	}
	return local, jar, nil
}

func inheritedFile(fd int, name string) (*os.File, error) {
	if fd < 3 {
		return nil, fmt.Errorf("missing %s descriptor", name)
	}
	f := os.NewFile(uintptr(fd), name)
	if f == nil {
		return nil, fmt.Errorf("invalid %s descriptor", name)
	}
	return f, nil
}

// openStateFile opens one owner-only regular file for read and write: no
// symlink in its place, mode 0600 (restored if looser), close-on-exec
// cleared for the macOS re-exec. It returns the resolved path the sandbox
// policy grants as a literal.
func openStateFile(path string) (*os.File, string, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|noFollow, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("state file: %w", err)
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", path)
	}
	if err == nil && info.Mode().Perm()&0o077 != 0 {
		err = f.Chmod(0o600)
	}
	if err == nil {
		err = inheritDiagnostic(f)
	}
	resolved := ""
	if err == nil {
		resolved, err = filepath.EvalSymlinks(path)
	}
	if err == nil {
		resolved, err = filepath.Abs(resolved)
	}
	if err != nil {
		f.Close()
		return nil, "", fmt.Errorf("state file: %w", err)
	}
	return f, resolved, nil
}

// loadState reads one header-prefixed payload from f. An empty file is a
// new store. Reads are bounded by the largest snapshot any store produces.
func loadState(f *os.File) ([]byte, error) {
	r := bufio.NewReader(io.NewSectionReader(f, 0, int64(stateHeaderLimit+maxStatePayload)))
	header, err := r.ReadString('\n')
	if err == io.EOF && header == "" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("unreadable header: %w", err)
	}
	length, convErr := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(header, stateMagic), "\n"))
	if !strings.HasPrefix(header, stateMagic) || convErr != nil || length < 0 || length > maxStatePayload {
		return nil, errors.New("unrecognized header")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("short payload: %w", err)
	}
	return data, nil
}

// stateSink rewrites f in place from offset 0: header, then the payload.
// Only write and sync happen on the descriptor after lockdown.
func stateSink(f *os.File) func([]byte) error {
	return func(data []byte) error {
		buf := make([]byte, 0, len(stateMagic)+24+len(data))
		buf = append(buf, stateMagic...)
		buf = strconv.AppendInt(buf, int64(len(data)), 10)
		buf = append(buf, '\n')
		buf = append(buf, data...)
		if _, err := f.WriteAt(buf, 0); err != nil {
			return err
		}
		_ = f.Sync() // best effort; the sandbox may refuse it, the data is written
		return nil
	}
}

// openLocalStore builds the loop's localStorage store: nil when the gate is
// closed, a memory store, or one loaded from f with a sink that rewrites it.
// A file that does not parse starts the store empty and is overwritten by
// the next write; the reason goes to stderr once.
func openLocalStore(p profile.Profile, f *os.File) (*sessionstorage.Session, func([]byte) error) {
	if p.LocalStorage != profile.LocalStorageOn {
		return nil, nil
	}
	store := sessionstorage.New(p.StorageBytes(), p.StorageTotalBytes())
	if p.Persist != profile.PersistFile || f == nil {
		return store, nil
	}
	data, err := loadState(f)
	if err == nil {
		err = store.Restore(data)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "local storage: starting empty:", err)
	}
	return store, stateSink(f)
}

// cookiePersister writes the cookie store's persistent cookies to its file,
// coalescing a burst of writes into one snapshot a second and once more at
// Close. Writes come from fetch workers and the VM goroutine, so it locks;
// the store's own lock guards Snapshot.
type cookiePersister struct {
	store *cookies.Store
	sink  func([]byte) error
	mu    sync.Mutex
	timer *time.Timer
	dirty bool
	fail  bool
}

// openCookieStore builds the cookie store for the profile: nil when cookies
// are off, memory-only unless persist=file gave it a file, in which case the
// persistent cookies load from f and changes flow back through a persister.
func openCookieStore(p profile.Profile, f *os.File) (*cookies.Store, *cookiePersister) {
	if p.Cookies != profile.CookiesSession {
		return nil, nil
	}
	store := cookies.New()
	if p.Persist != profile.PersistFile || f == nil {
		return store, nil
	}
	data, err := loadState(f)
	if err == nil {
		err = store.Restore(data)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cookies: starting empty:", err)
	}
	cp := &cookiePersister{store: store, sink: stateSink(f)}
	store.OnChange(cp.changed)
	return store, cp
}

func (cp *cookiePersister) changed() {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.dirty = true
	if cp.timer == nil {
		cp.timer = time.AfterFunc(stateFlushDelay, cp.flush)
	}
}

func (cp *cookiePersister) flush() {
	cp.mu.Lock()
	cp.timer = nil
	if !cp.dirty {
		cp.mu.Unlock()
		return
	}
	cp.dirty = false
	cp.mu.Unlock()
	if err := cp.sink(cp.store.Snapshot()); err != nil {
		cp.mu.Lock()
		first := !cp.fail
		cp.fail = true
		cp.mu.Unlock()
		if first {
			fmt.Fprintln(os.Stderr, "cookies not saved:", err)
		}
	}
}

// Close stops the timer and writes the final snapshot.
func (cp *cookiePersister) Close() {
	if cp == nil {
		return
	}
	cp.mu.Lock()
	if cp.timer != nil {
		cp.timer.Stop()
		cp.timer = nil
	}
	cp.dirty = true // the final write is unconditional: it also prunes expired cookies from the file
	cp.mu.Unlock()
	cp.flush()
}
