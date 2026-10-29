package loop

import "nimbleterminal.dev/nthtml/document"

// fallbackFaces are the host fonts nthtml tries for characters the page's
// faces lack. Build-tagged files add to it in init; it is read-only after.
var fallbackFaces []document.FallbackFace
