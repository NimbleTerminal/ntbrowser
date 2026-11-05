module nimbleterminal.dev/ntbrowser

go 1.26.8

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.1.1
	github.com/charmbracelet/x/ansi v0.11.9
	github.com/dop251/goja v0.0.0-20261105002551-2413f2132ac8
	golang.org/x/image v0.47.0
	golang.org/x/net v0.62.0
	nimbleterminal.dev/ntfonts/cjk v0.3.1
	nimbleterminal.dev/nthtml v0.2.2
	nimbleterminal.dev/ntmediabox v0.1.0
	nimbleterminal.dev/ntpicture v0.1.5
)

require (
	charm.land/lipgloss/v2 v2.0.6 // indirect
	github.com/NimbleMarkets/oksvg v0.1.6 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20261023060817-a45b7c86333f // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dlclark/regexp2/v2 v2.12.0 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/google/pprof v0.0.0-20261105003125-4bf881d7fb0b // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.31 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef // indirect
	github.com/xo/terminfo v1.3.1 // indirect
	github.com/zkrebbekx/go-mermaid v0.1.6 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	nimbleterminal.dev/ntconfine v0.1.1 // indirect
	nimbleterminal.dev/ntconfine/pkg/confine v0.1.0 // indirect
	nimbleterminal.dev/ntfonts v0.3.1 // indirect
	nimbleterminal.dev/ntmediabox/pkg/mediabox v0.1.0 // indirect
	nimbleterminal.dev/ntsvg v0.6.0 // indirect
)

// WASM-capable Bubble Tea pin required by the sandbox compile-only gate.
replace charm.land/bubbletea/v2 => github.com/neomantra/bubbletea/v2 v2.0.0-20261020122444-536722b0bdb6

// Vector rendering fork used by nttypeset and ntsvg (same as nthtml).
replace github.com/srwiley/rasterx => github.com/neomantra/rasterx v0.1.0

replace nimbleterminal.dev/ntmediabox => ../ntmediabox

replace nimbleterminal.dev/ntmediabox/pkg/mediabox => ../ntmediabox/pkg/mediabox

replace nimbleterminal.dev/ntconfine => ../ntconfine

replace nimbleterminal.dev/ntconfine/pkg/confine => ../ntconfine/pkg/confine

// Exercise unreleased sibling HEADs; switch to release versions after validation.
replace nimbleterminal.dev/nthtml => ../nthtml

replace nimbleterminal.dev/nttypeset => ../nttypeset
