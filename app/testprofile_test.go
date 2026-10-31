package app

import "nimbleterminal.dev/ntbrowser/profile"

var testLoopProfile, _ = profile.Builtin("browse")

func testProfile(hosts []string, private bool, nav NavMode) profile.Profile {
	p, _ := profile.Builtin("browse")
	p.Name, p.Hosts, p.PrivateNetwork, p.Navigate = "test", hosts, private, nav
	return p
}
