// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import "errors"

// Only presence selectors are supported inside brackets. Preserve their CSS
// whitespace without treating it as a descendant combinator. Bound work before
// allocating predicates; escapes, values and namespaces are rejected downstream.
func selectorParts(selector string) ([]string, error) {
	if len(selector) > 4096 {
		return nil, errors.New("selector too long")
	}
	var parts []string
	start := -1
	bracket := false
	for i, c := range selector {
		switch c {
		case '[':
			if bracket {
				return nil, errors.New("nested attribute selector")
			}
			bracket = true
		case ']':
			if !bracket {
				return nil, errors.New("unmatched attribute selector")
			}
			bracket = false
		case ' ', '\t', '\r', '\n', '\f':
			if !bracket {
				if start >= 0 {
					parts = append(parts, selector[start:i])
					start = -1
					if len(parts) > 8 {
						return nil, errors.New("too many selector parts")
					}
				}
				continue
			}
		}
		if start < 0 {
			start = i
		}
	}
	if bracket {
		return nil, errors.New("unclosed attribute selector")
	}
	if start >= 0 {
		parts = append(parts, selector[start:])
	}
	return parts, nil
}
