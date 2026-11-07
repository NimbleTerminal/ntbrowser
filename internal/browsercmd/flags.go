// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package browsercmd

import (
	"strings"

	"github.com/spf13/pflag"
)

// Retain existing -long invocations while pflag provides --long, --long=value,
// -h and interspersed options. Do not rewrite option values or anything after
// the explicit -- positional separator.
func legacyLongFlags(fs *pflag.FlagSet, args []string) []string {
	out := append([]string(nil), args...)
	value := false
	for i, arg := range out {
		if value {
			value = false
			continue
		}
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		name := strings.TrimPrefix(arg, "-")
		name = strings.TrimPrefix(name, "-")
		name, _, equals := strings.Cut(name, "=")
		f := fs.Lookup(name)
		if f == nil && len(name) == 1 && !strings.HasPrefix(arg, "--") {
			if short := fs.ShorthandLookup(name); short != nil {
				value = !equals && short.NoOptDefVal == ""
				continue
			}
		}
		if f == nil && arg == "-help" {
			out[i] = "--help"
		}
		if f == nil {
			continue
		}
		if !strings.HasPrefix(arg, "--") {
			out[i] = "-" + arg
		}
		value = !equals && f.NoOptDefVal == ""
	}
	return out
}
