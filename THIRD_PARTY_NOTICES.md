# Third-party notices

ntbrowser links the following modules. Their license texts are in `licenses/`.

| Module | Version | License | File |
| --- | --- | --- | --- |
| github.com/dop251/goja | v0.0.0-20261027234916-5f913d1693d9 | MIT | [licenses/goja.LICENSE](licenses/goja.LICENSE) |
| golang.org/x/net | v0.62.0 | BSD-3-Clause | [licenses/golang-x-net.LICENSE](licenses/golang-x-net.LICENSE) (also covers the SVG name tables copied into `dom/svgcase.go`) |
| github.com/charmbracelet/x/ansi | v0.11.9 | MIT | [licenses/charmbracelet-x-ansi.LICENSE](licenses/charmbracelet-x-ansi.LICENSE) |
| charm.land/bubbletea/v2 (replaced by github.com/neomantra/bubbletea/v2) | v2.0.0-20261020122444-536722b0bdb6 | MIT | [licenses/bubbletea.LICENSE](licenses/bubbletea.LICENSE) |
| nimbleterminal.dev/nthtml | v0.1.3-0.20261029010611-9bbfc5b0ae0e | MIT | [licenses/nthtml.LICENSE](licenses/nthtml.LICENSE) |
| nimbleterminal.dev/ntpicture | v0.1.3 | MIT | [licenses/ntpicture.LICENSE](licenses/ntpicture.LICENSE) |
| nimbleterminal.dev/ntfonts | v0.1.1-0.20261029000300-c0ab5130e225 | MIT (module); embedded fonts SIL OFL-1.1 | [licenses/ntfonts.LICENSE](licenses/ntfonts.LICENSE); font licenses in [licenses/fonts/](licenses/fonts/) |
| golang.org/x/image | v0.47.0 | BSD-3-Clause | [licenses/golang-x-image.LICENSE](licenses/golang-x-image.LICENSE) |

The binary embeds the ntfonts subset faces (Literata, Newsreader, IBM Plex Sans, Source Serif 4, Iosevka, IBM Plex Mono, JetBrains Mono, Noto Sans Symbols 2 with Noto Sans Math glyphs) through nthtml; each family's OFL-1.1 text is in `licenses/fonts/`. The Go fonts drawn as the last fallback come from golang.org/x/image; their license is `licenses/fonts/gofont.LICENSE`.

Transitive dependencies carry their own licenses in the Go module cache; see `go.mod`.
