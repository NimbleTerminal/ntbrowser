// Package sandbox applies the operating system's half of a profile: once, at
// startup, irreversibly, before any page content is touched. What it reports
// comes from what was actually applied, because a gate reported stronger than
// it is would be a security bug. See the profiles design, decisions 8–11.
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntmediabox"
)

// SandboxedArg marks the re-executed process; its value carries the state the
// run decided before lockdown, so nothing is read again inside the sandbox.
const SandboxedArg = "--sandboxed"

// StageArg is the hidden first argument of the Linux helper stage.
const StageArg = ntmediabox.StageArg

// Helper is the running media helper. Conn is the main process's end of the
// socket; its descriptor is carried across the macOS re-exec.
type Helper = ntmediabox.Helper

// HelperPath is ntbrowser-media beside this executable.
func HelperPath() (string, error) {
	path, err := ntmediabox.ResolveSibling("ntbrowser-media")
	if err != nil {
		return "", fmt.Errorf("media=video needs ntbrowser-media: %w", err)
	}
	return path, nil
}

// StartHelper starts the decoder process, before the main process locks
// itself down, so the main process never needs permission to start anything.
// The helper gets the socket as fd 3, an empty environment, no terminal, and a
// sandbox narrower than the main process's own.
func StartHelper(p profile.Profile, helper, tempDir string) (*Helper, error) {
	pol := ntmediabox.DefaultPolicy(helper, tempDir)
	pol.AllowUnsandboxed = p.Enforcement == profile.EnforceBestEffort
	stagePath, _ := os.Executable()
	if sibling := filepath.Join(filepath.Dir(helper), "ntbrowser"); sibling != helper {
		if st, err := os.Stat(sibling); err == nil && st.Mode().IsRegular() {
			stagePath = sibling
		}
	}
	h, err := ntmediabox.StartHelper(ntmediabox.HelperConfig{
		Policy:     pol,
		WorkerPath: helper,
		WorkerArgs: []string{tempDir},
		StagePath:  stagePath,
	})
	if errors.Is(err, ntmediabox.ErrUnavailable) {
		return nil, ErrUnavailable
	}
	return h, err
}

// KillHelper ends a helper by process id, for a process that holds no Helper.
func KillHelper(pid int) { ntmediabox.KillHelper(pid) }

// MediaStage confines this process and replaces it with the helper. It
// returns only on failure.
func MediaStage(helper, arg string) error {
	err := ntmediabox.RunStage([]string{StageArg, arg})
	if err != nil {
		if st, statErr := os.Stat(arg); statErr == nil && st.IsDir() {
			return ntmediabox.RunStagePolicy(ntmediabox.DefaultPolicy(helper, arg), helper, []string{arg})
		}
	}
	return err
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
