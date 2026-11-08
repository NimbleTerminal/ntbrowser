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

	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sessionstorage"
)

// localStoreMagic heads the store file; the number after it is the payload
// length, so a rewrite never needs truncation and stale bytes past the
// payload are ignored.
const localStoreMagic = "glynx-local-storage v1 "

// defaultLocalStorePath is $XDG_DATA_HOME/<command>/local-storage.dat, with
// the platform's data directory when XDG_DATA_HOME is unset.
func defaultLocalStorePath(command string) (string, error) {
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
	return filepath.Join(base, command, "local-storage.dat"), nil
}

// localStoreFile opens the local-storage=file store, only before lockdown,
// like the diagnostics file: one owner-only regular file (no symlinks, mode
// 0600, parent 0700), whose inherited descriptor is the only write path the
// sandboxed process keeps. Nothing else under the directory is reachable.
func localStoreFile(st *state, sandboxed bool) (*os.File, error) {
	if st.Config.Profile.LocalStorage != profile.LocalStorageFile {
		return nil, nil
	}
	if sandboxed {
		if st.StorageFD < 3 {
			return nil, fmt.Errorf("missing local storage descriptor")
		}
		f := os.NewFile(uintptr(st.StorageFD), "local-storage")
		if f == nil {
			return nil, fmt.Errorf("invalid local storage descriptor")
		}
		return f, nil
	}
	path := st.Config.LocalStorageFile
	if path == "" {
		return nil, errors.New("local-storage=file needs --local-storage-file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("local storage: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|noFollow, 0o600)
	if err != nil {
		return nil, fmt.Errorf("local storage: %w", err)
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
	if err == nil {
		st.Config.LocalStorageFile, err = filepath.EvalSymlinks(path)
	}
	if err == nil {
		st.Config.LocalStorageFile, err = filepath.Abs(st.Config.LocalStorageFile)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("local storage: %w", err)
	}
	st.StorageFD = int(f.Fd())
	return f, nil
}

// openLocalStore builds the loop's localStorage store for the profile: a
// memory store, or the file store loaded from f with a sink that rewrites
// it. A file that does not parse starts the store empty and is overwritten
// by the next write; the reason goes to stderr once.
func openLocalStore(p profile.Profile, f *os.File) (*sessionstorage.Session, func([]byte) error) {
	switch p.LocalStorage {
	case profile.LocalStorageMemory:
		return sessionstorage.New(p.StorageBytes(), p.StorageTotalBytes()), nil
	case profile.LocalStorageFile:
		if f == nil {
			return nil, nil
		}
		store := sessionstorage.New(p.StorageBytes(), p.StorageTotalBytes())
		if err := loadLocalStore(store, f); err != nil {
			fmt.Fprintln(os.Stderr, "local storage: starting empty:", err)
		}
		return store, localStoreSink(f)
	}
	return nil, nil
}

// loadLocalStore reads one header-prefixed snapshot from f into store. An
// empty file is a new store. Reads are bounded by the snapshot ceiling.
func loadLocalStore(store *sessionstorage.Session, f *os.File) error {
	r := bufio.NewReader(io.NewSectionReader(f, 0, int64(len(localStoreMagic))+32+sessionstorage.MaxSnapshotBytes))
	header, err := r.ReadString('\n')
	if err == io.EOF && header == "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("unreadable header: %w", err)
	}
	length, convErr := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(header, localStoreMagic), "\n"))
	if !strings.HasPrefix(header, localStoreMagic) || convErr != nil || length < 0 || length > sessionstorage.MaxSnapshotBytes {
		return errors.New("unrecognized header")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("short snapshot: %w", err)
	}
	return store.Restore(data)
}

// localStoreSink rewrites the file in place from offset 0: header, then the
// snapshot. Only write and sync happen on the descriptor after lockdown.
func localStoreSink(f *os.File) func([]byte) error {
	return func(data []byte) error {
		buf := make([]byte, 0, len(localStoreMagic)+24+len(data))
		buf = append(buf, localStoreMagic...)
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
