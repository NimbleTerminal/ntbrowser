// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package subres

import "time"

// patience is how long a test waits for something that must happen before it
// gives up. It is never reached when the test passes, so it is generous: a
// shared CI runner under the race detector can be ten times slower than a
// developer's machine. Waits that assert nothing happens stay short and
// explicit.
const patience = time.Minute
