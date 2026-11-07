// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"errors"
	"strings"

	"nimbleterminal.dev/nthtml/document"
)

const diagramFallbackWarning = "diagram budget exceeded; SVG labels and diagram source shown instead"

// nthtml v0.5.0 exposes ErrLimit without a typed resource detail. Match only
// its two aggregate diagram budgets; unrelated limits must still fail closed.
func diagramBudgetExceeded(err error) bool {
	return errors.Is(err, document.ErrLimit) &&
		(strings.HasSuffix(err.Error(), ": diagram count") || strings.HasSuffix(err.Error(), ": diagram pixels"))
}
