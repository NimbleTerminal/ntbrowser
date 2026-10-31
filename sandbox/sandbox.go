// Package sandbox applies the operating system's half of a profile: once, at
// startup, irreversibly, before any page content is touched. What it reports
// comes from what was actually applied, because a gate reported stronger than
// it is would be a security bug. See the profiles design, decisions 8–11.
package sandbox

import (
	"errors"

	"nimbleterminal.dev/ntbrowser/profile"
)

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
	if !applied {
		return profile.Strengths{}
	}
	return profile.Strengths{"files": profile.StrengthOS, "process": profile.StrengthOS, "native-code": profile.StrengthOS, "environment": profile.StrengthChecked}
}

// Probe reports the strengths this machine could apply, for -show-profile.
func Probe() profile.Strengths { return strengths(Available()) }
