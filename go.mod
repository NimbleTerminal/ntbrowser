module nimbleterminal.dev/ntbrowser

go 1.26.8

require (
	charm.land/bubbletea/v2 v2.1.0
	github.com/charmbracelet/x/ansi v0.11.9
	github.com/dop251/goja v0.0.0-20261024144015-2a9269b6b6de
	golang.org/x/net v0.61.0
	nimbleterminal.dev/nthtml v0.1.0
)

require (
	charm.land/bubbles/v2 v2.2.1 // indirect
	charm.land/lipgloss/v2 v2.0.6 // indirect
	github.com/NimbleMarkets/oksvg v0.1.2 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20261008173134-6b8d4baf91b4 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dlclark/regexp2/v2 v2.8.1 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.31 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef // indirect
	github.com/xo/terminfo v1.2.0 // indirect
	github.com/zkrebbekx/go-mermaid v0.1.6 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	nimbleterminal.dev/ntfonts v0.1.0 // indirect
	nimbleterminal.dev/ntpicture v0.1.1 // indirect
	nimbleterminal.dev/ntsvg v0.5.2 // indirect
)

// Same WASM-capable Bubble Tea pin as nthtml and ntvideo.
replace charm.land/bubbletea/v2 => github.com/neomantra/bubbletea/v2 v2.0.0-20261009201241-c397c32a5074
