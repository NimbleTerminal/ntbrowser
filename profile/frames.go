// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package profile

import "nimbleterminal.dev/ntbrowser/origin"

type Frames string

const (
	FramesAny       Frames = "any"
	FramesOff       Frames = "off"
	FramesOrigin    Frames = "origin"
	FramesAllowlist Frames = "allowlist"
)

func (p Profile) FrameOrigins(source string) []string {
	if p.Frames != FramesAllowlist {
		return nil
	}
	canonical, err := origin.Of(source)
	if err != nil {
		return nil
	}
	var out []string
	for _, g := range p.FrameGrants {
		normalized, err := g.Normalized()
		if err == nil && normalized.Source == canonical {
			out = append(out, normalized.Destination)
		}
	}
	return out
}
func (p Profile) FramesOn() bool {
	return p.Frames == FramesOrigin || p.Frames == FramesAllowlist || p.Frames == FramesAny
}
