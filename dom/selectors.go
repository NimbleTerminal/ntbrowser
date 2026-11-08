// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"errors"
	"strings"
)

// selectorGroups splits a selector list at commas outside brackets and quoted
// values, at most eight groups, none empty. The byte bound applies to the
// whole list before any group is parsed.
func selectorGroups(selector string) ([]string, error) {
	if len(selector) > 4096 {
		return nil, errors.New("selector too long")
	}
	var groups []string
	start := 0
	bracket := false
	var quote rune
	for i, c := range selector {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			if bracket {
				quote = c
			}
		case c == '[':
			bracket = true
		case c == ']':
			bracket = false
		case c == ',' && !bracket:
			groups = append(groups, selector[start:i])
			start = i + 1
			if len(groups) >= 8 {
				return nil, errors.New("too many selector groups")
			}
		}
	}
	groups = append(groups, selector[start:])
	for i, g := range groups {
		if strings.Trim(g, " \t\r\n\f") == "" {
			return nil, errors.New("empty selector group")
		}
		groups[i] = g
	}
	return groups, nil
}

// Brackets hold presence and value selectors. Preserve their CSS whitespace,
// and quoted values' brackets, without treating them as combinators. Bound
// work before allocating predicates; escapes and namespaces are rejected
// downstream.
func selectorParts(selector string) ([]string, error) {
	if len(selector) > 4096 {
		return nil, errors.New("selector too long")
	}
	var parts []string
	start := -1
	bracket := false
	var quote rune
	for i, c := range selector {
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			if bracket {
				quote = c
			}
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
	if bracket || quote != 0 {
		return nil, errors.New("unclosed attribute selector")
	}
	if start >= 0 {
		parts = append(parts, selector[start:])
	}
	return parts, nil
}
