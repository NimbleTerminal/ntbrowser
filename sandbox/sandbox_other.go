//go:build !darwin && !linux

package sandbox

import "nimbleterminal.dev/ntbrowser/profile"

// Available reports whether this platform can enforce a profile.
func Available() bool { return false }

// StartHelper cannot confine a helper here, so there is none.
func StartHelper(profile.Profile, string, string) (*Helper, error) { return nil, ErrUnavailable }

// KillHelper has nothing to do here.
func KillHelper(int) {}

// InheritableFD does not exist here.
func (*Helper) InheritableFD() (int, error) { return 0, ErrUnavailable }

// MediaStage does not exist here.
func MediaStage(string, string) error { return ErrUnavailable }

// Reexec does nothing here.
func Reexec(profile.Profile, string, string, []string) error { return nil }

// Apply reports that nothing was applied.
func Apply(p profile.Profile, _, _ string, _ bool) (profile.Strengths, error) {
	return strengths(false), checkRequired(p, false)
}
