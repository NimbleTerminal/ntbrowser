// Package sandbox applies the operating system's half of a profile: once, at
// startup, irreversibly, before any page content is touched. What it reports
// comes from what was actually applied, because a gate reported stronger than
// it is would be a security bug. See the profiles design, decisions 8–11.
package sandbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"nimbleterminal.dev/ntbrowser/profile"
)

// StageArg is the hidden first argument of the Linux helper stage.
const StageArg = "--media-stage"

// Helper is the running media helper. Conn is the main process's end of the
// socket; fd is its descriptor, carried across the macOS re-exec.
type Helper struct {
	Conn io.ReadWriteCloser
	cmd  *exec.Cmd
	file *os.File
}

// Close ends the helper.
func (h *Helper) Close() error {
	err := h.Conn.Close()
	if h.cmd != nil && h.cmd.Process != nil {
		h.cmd.Process.Kill()
		h.cmd.Wait()
	}
	return err
}

// HelperPath is ntbrowser-media beside this executable.
func HelperPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(exe), "ntbrowser-media")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("media=video needs %s: %w", path, err)
	}
	return path, nil
}

// ErrUnavailable means this machine cannot enforce the profile.
var ErrUnavailable = errors.New("sandbox: OS enforcement is not available here")

// checkRequired applies the enforcement field: required refuses to run
// without OS enforcement; best-effort runs on checked gates alone.
func checkRequired(p profile.Profile, applied bool) error {
	if !applied && p.Enforcement != profile.EnforceBestEffort {
		return ErrUnavailable
	}
	return nil
}

// strengths reports the gates the kernel holds.
func strengths(applied bool) profile.Strengths {
	s := profile.Strengths{"native-code": profile.StrengthCompiledOut} // this binary links none
	if applied {
		s["files"], s["process"] = profile.StrengthOS, profile.StrengthOS
	}
	return s
}

// Probe reports the strengths this machine could apply, for -show-profile.
func Probe() profile.Strengths { return strengths(Available()) }
