package loop

import "nimbleterminal.dev/ntbrowser/profile"

var testProfile, externalProfile = func() (profile.Profile, profile.Profile) {
	p, _ := profile.Builtin("browse")
	e := p
	e.Scripts = profile.ScriptsExternal
	return p, e
}()
