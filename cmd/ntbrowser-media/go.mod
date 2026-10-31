module nimbleterminal.dev/ntbrowser/cmd/ntbrowser-media

go 1.26.8

require (
	nimbleterminal.dev/ntbrowser v0.0.0
	nimbleterminal.dev/ntvideo v0.3.0
)

require (
	charm.land/bubbletea/v2 v2.1.1 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20261023060817-a45b7c86333f // indirect
	github.com/charmbracelet/x/ansi v0.11.9 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/go-webgpu/goffi v0.6.7 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.31 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v1.3.0 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	nimbleterminal.dev/ntpicture v0.1.4 // indirect
)

replace nimbleterminal.dev/ntbrowser => ../..

// Local until ntvideo's Player is tagged.
replace nimbleterminal.dev/ntvideo => ../../../ntvideo

replace charm.land/bubbletea/v2 => github.com/neomantra/bubbletea/v2 v2.0.0-20261020122444-536722b0bdb6

replace github.com/srwiley/rasterx => github.com/neomantra/rasterx v0.1.0
