// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	weburl "github.com/nlnwa/whatwg-url/url"
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/nthtml/document"
)

// anchorHref uses the same URL interpretation as HTMLAnchorElement, then
// applies the viewer's openable-link filter. This is not authorization:
// NavigateMsg still goes through app's profile gate and netpolicy's checks.
func anchorHref(page *Page, n *dom.Node) string {
	raw, ok := n.Attr("href")
	if !ok {
		return ""
	}
	u, err := weburl.ParseRef(page.Base, raw)
	if err != nil {
		return ""
	}
	return document.SafeURL(u.Href(false))
}
