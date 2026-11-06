// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Open only before lockdown. O_EXCL refuses existing files and symlinks. The
// inherited descriptor grants writes to this one file, never a directory.
func diagnosticFile(st *state, sandboxed bool) (*os.File, error) {
	if !st.Config.Profile.Diagnostics {
		return nil, nil
	}
	if sandboxed {
		if st.DiagnosticFD < 3 {
			return nil, fmt.Errorf("missing diagnostic descriptor")
		}
		f := os.NewFile(uintptr(st.DiagnosticFD), "diagnostics")
		if f == nil {
			return nil, fmt.Errorf("invalid diagnostic descriptor")
		}
		return f, nil
	}
	f, err := os.OpenFile(st.Config.Diagnostics, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if err = inheritDiagnostic(f); err != nil {
		f.Close()
		return nil, err
	}
	st.Config.Diagnostics, err = filepath.EvalSymlinks(st.Config.Diagnostics)
	if err == nil {
		st.Config.Diagnostics, err = filepath.Abs(st.Config.Diagnostics)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	st.DiagnosticFD = int(f.Fd())
	return f, nil
}
