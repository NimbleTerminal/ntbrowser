// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import "nimbleterminal.dev/ntbrowser/profile"

var testProfile, externalProfile = func() (profile.Profile, profile.Profile) {
	p, _ := profile.Builtin("browse")
	e := p
	e.Scripts = profile.ScriptsExternal
	return p, e
}()
