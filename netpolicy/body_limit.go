// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package netpolicy

import (
	"fmt"
	"net/url"
)

// BodyLimitError preserves ErrTooLarge matching while identifying the resource
// and actual boundary. Observed is a lower bound when reading stops at limit+1.
// URL is the final vetted URL; display omits credentials, query and fragment.
type BodyLimitError struct {
	Resource, URL   string
	Limit, Observed int64
	AtLeast         bool
	Request         bool
}

func (e *BodyLimitError) Error() string {
	prefix := ErrTooLarge.Error()
	if e.Request {
		prefix = "ntbrowser/netpolicy: request body too large"
	}
	resource := e.Resource
	if resource == "" {
		resource = "body"
	}
	qualifier := ""
	if e.AtLeast {
		qualifier = "at least "
	}
	message := fmt.Sprintf("%s: %s limit %d bytes, observed %s%d bytes", prefix, resource, e.Limit, qualifier, e.Observed)
	if u, err := url.Parse(e.URL); err == nil && e.URL != "" {
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		u.RawFragment = ""
		location := u.String()
		if len(location) > 256 {
			location = location[:256] + "…"
		}
		message += " at " + location
	}
	return message
}
func (e *BodyLimitError) Unwrap() error { return ErrTooLarge }
func bodyResource(k Kind) string {
	switch k {
	case KindStyle:
		return "stylesheet"
	case KindImage:
		return "image"
	case KindNavigate:
		return "page"
	case KindScript:
		return "script"
	default:
		return "fetch/document"
	}
}
