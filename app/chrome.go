// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

var (
	devtoolsHeaderStyle = ansi.NewStyle().Bold().ForegroundColor(ansi.BrightWhite).BackgroundColor(ansi.Blue)
	errorLineStyle      = ansi.NewStyle().ForegroundColor(ansi.BrightWhite).BackgroundColor(ansi.Red)
	warningLineStyle    = ansi.NewStyle().ForegroundColor(ansi.Black).BackgroundColor(ansi.Yellow)
)

// chromeLine cleans page text before adding host-owned styling. Pad by display
// cells so the background spans the row, and reset it before the following row.
func chromeLine(line string, width int, style ansi.Style) string {
	width = max(1, width)
	line = ansi.Truncate(strings.ReplaceAll(clean(line), "\n", " "), width, "…")
	line += strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
	return style.Styled(line)
}

func statusLine(s status, width int) string {
	if style, ok := s.style(); ok {
		return chromeLine(s.text, width, style)
	}
	return ansi.Truncate(clean(s.text), max(1, width), "…")
}
