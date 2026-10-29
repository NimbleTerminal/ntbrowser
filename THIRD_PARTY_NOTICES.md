# Third-party notices

ntbrowser links the following modules. Their license texts are in `licenses/`.

| Module | Version | License | File |
| --- | --- | --- | --- |
| github.com/dop251/goja | v0.0.0-20261029153331-f81816846776 | MIT | [licenses/goja.LICENSE](licenses/goja.LICENSE) |
| golang.org/x/net | v0.62.0 | BSD-3-Clause | [licenses/golang-x-net.LICENSE](licenses/golang-x-net.LICENSE) (also covers the SVG name tables copied into `dom/svgcase.go`) |
| github.com/charmbracelet/x/ansi | v0.11.9 | MIT | [licenses/charmbracelet-x-ansi.LICENSE](licenses/charmbracelet-x-ansi.LICENSE) |
| charm.land/bubbletea/v2 (replaced by github.com/neomantra/bubbletea/v2) | v2.0.0-20261020122444-536722b0bdb6 | MIT | [licenses/bubbletea.LICENSE](licenses/bubbletea.LICENSE) |
| nimbleterminal.dev/nthtml | v0.1.3-0.20261029152251-efd6ec4ef906 | MIT | [licenses/nthtml.LICENSE](licenses/nthtml.LICENSE) |
| nimbleterminal.dev/ntpicture | v0.1.3 | MIT | [licenses/ntpicture.LICENSE](licenses/ntpicture.LICENSE) |
| nimbleterminal.dev/ntfonts | v0.2.0 | MIT (module); embedded fonts SIL OFL-1.1 | [licenses/ntfonts.LICENSE](licenses/ntfonts.LICENSE); font licenses in [licenses/fonts/](licenses/fonts/) |
| nimbleterminal.dev/ntfonts/cjk | v0.1.0 | MIT (module); embedded font SIL OFL-1.1 | [licenses/ntfonts.LICENSE](licenses/ntfonts.LICENSE); font license [licenses/fonts/notosanscjksc.OFL.txt](licenses/fonts/notosanscjksc.OFL.txt) |
| golang.org/x/image | v0.47.0 | BSD-3-Clause | [licenses/golang-x-image.LICENSE](licenses/golang-x-image.LICENSE) |

The binary embeds the ntfonts subset faces (Literata, Newsreader, IBM Plex Sans, Source Serif 4, Iosevka, IBM Plex Mono, JetBrains Mono, Noto Sans Symbols 2 with Noto Sans Math glyphs) through nthtml; each family's OFL-1.1 text is in `licenses/fonts/`. The Go fonts drawn as the last fallback come from golang.org/x/image; their license is `licenses/fonts/gofont.LICENSE`. Unless built with `-tags nocjk`, the binary also embeds the ntfonts/cjk Simplified Chinese Regular subset (Noto Sans CJK SC, renamed Gloss Text CJKSC); its OFL-1.1 text is `licenses/fonts/notosanscjksc.OFL.txt`.

Transitive dependencies carry their own licenses in the Go module cache; see `go.mod`.
