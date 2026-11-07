// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import "testing"

func TestInlineStyleParsing(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"width: 2em; height: 1e2px", "width: 2em; height: 1e2px;"},
		{"width: 3px !important; width: 4px; height: invalid", "width: 3px !important;"},
		{`background-image: url("a;b!c"); --Case: {x:y};`, `background-image: url("a;b!c"); --Case: {x:y};`},
		{"width: 2px; /*unfinished", "width: 2px;"},
		{"width: NaN; height: Infpx", ""},
		{"color: /*comment*/ red", "color: red;"},
	} {
		if got := SerializeInlineStyle(ParseInlineStyle(tc.input)); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.input, got, tc.want)
		}
	}
}
