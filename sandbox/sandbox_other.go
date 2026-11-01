//go:build !darwin && !linux

package sandbox

import "nimbleterminal.dev/ntbrowser/profile"

// Available reports whether this platform can enforce a profile.
func Available() bool { return false }

// Reexec does nothing here.
func Reexec(profile.Profile, string, string, []string) error { return nil }

// Apply reports that nothing was applied.
func Apply(p profile.Profile, _, _ string, _ bool) (profile.Strengths, error) {
	return strengths(false), checkRequired(p, false)
}
