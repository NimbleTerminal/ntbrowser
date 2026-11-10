// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import "github.com/charmbracelet/x/ansi"

// statusKind says where a status line came from. What may replace a status
// and how it is drawn follow from the kind alone, never from its text, which
// is often page-controlled.
type statusKind uint8

const (
	statusPlain   statusKind = iota // host notes, prompts, "loading …", console output
	statusFocus                     // the focused control; survives the page's first frame
	statusWarning                   // page, image and console warnings
	statusPaint                     // a failed band paint; the next good paint or frame clears it
	statusRender                    // a failed frame; the next good frame clears it
	statusError                     // a refused reader or script action; replaced like a note
	statusFailure                   // a console error, failed load or blocked navigation; frames never replace it
)

type status struct {
	kind statusKind
	text string // already through clean
}

// sticky reports whether frames and their warnings must leave the status
// alone. Only a navigation or a newer message replaces it.
func (s status) sticky() bool { return s.kind == statusFailure }

func (s status) style() (ansi.Style, bool) {
	switch s.kind {
	case statusFailure, statusError, statusRender, statusPaint:
		return errorLineStyle, true
	case statusWarning:
		return warningLineStyle, true
	}
	return ansi.Style{}, false
}

// setStatus is the one way text reaches the status line: through clean.
func (m *Model) setStatus(kind statusKind, text string) {
	m.status = status{kind, clean(text)}
}

func (m *Model) clearStatus() { m.status = status{} }

// consoleStatus maps a console level to its kind.
func consoleStatus(level string) statusKind {
	switch level {
	case "error":
		return statusFailure
	case "warn", "warning":
		return statusWarning
	}
	return statusPlain
}
