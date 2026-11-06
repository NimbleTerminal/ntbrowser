// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package profile

import (
	"fmt"
	"nimbleterminal.dev/ntbrowser/origin"
	"strings"
)

type ScriptGrant struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func ParseScriptGrant(value string) (ScriptGrant, error) {
	source, destination, ok := strings.Cut(value, "=")
	if !ok {
		return ScriptGrant{}, fmt.Errorf("script grant must be SOURCE_ORIGIN=DESTINATION_ORIGIN")
	}
	return (ScriptGrant{Source: source, Destination: destination}).Normalized()
}
func (g ScriptGrant) Normalized() (ScriptGrant, error) {
	source, err := origin.Parse(g.Source)
	if err != nil {
		return ScriptGrant{}, fmt.Errorf("script grant source: %w", err)
	}
	destination, err := origin.Parse(g.Destination)
	if err != nil {
		return ScriptGrant{}, fmt.Errorf("script grant destination: %w", err)
	}
	return ScriptGrant{Source: source, Destination: destination}, nil
}
func (p Profile) ScriptOrigins(source string) []string {
	var out []string
	canonical, err := origin.Of(source)
	if err != nil {
		return nil
	}
	for _, g := range p.ScriptGrants {
		normalized, err := g.Normalized()
		if err == nil && normalized.Source == canonical {
			out = append(out, normalized.Destination)
		}
	}
	return out
}
