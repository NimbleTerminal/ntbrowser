// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import "nimbleterminal.dev/ntbrowser/profile"

var testLoopProfile, _ = profile.Builtin("browse")

func testProfile(hosts []string, private bool, nav NavMode) profile.Profile {
	p, _ := profile.Builtin("browse")
	p.Name, p.Hosts, p.PrivateNetwork, p.Navigate = "test", hosts, private, nav
	return p
}
