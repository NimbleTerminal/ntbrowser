// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package ntbrowser is a secure, non-JIT terminal web runtime: a hardened
// Goja (pure-Go ECMAScript interpreter) sandbox drives a mutable DOM owned by
// ntbrowser, and nthtml renders that DOM in the terminal. JavaScript has no
// access to the operating system, the network (beyond a policy-filtered
// fetch), or nthtml itself; every frame re-serializes the tree to HTML and
// nthtml re-renders it off the UI goroutine.
//
// See docs/design/2026-10-25-ntbrowser-architecture-design.md for
// the design this module implements.
package ntbrowser
