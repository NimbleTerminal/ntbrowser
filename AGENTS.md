# AGENTS.md

Project rules for agents and humans. The design document `docs/design/2026-10-25-ntbrowser-architecture-design.md` is normative; read it before changing `jsvm`, `netpolicy`, `dom` budgets or `app.clean`.

## Constraints

- Module path `nimbleterminal.dev/ntbrowser`; `go 1.26.8`; `go.mod` is spec Appendix B verbatim, including `replace charm.land/bubbletea/v2 => github.com/neomantra/bubbletea/v2 v2.0.0-20261020122444-536722b0bdb6` and `replace github.com/srwiley/rasterx => github.com/neomantra/rasterx v0.1.0` (both mirror nthtml's go.mod; a dependency's replaces do not apply here) and `require nimbleterminal.dev/nthtml v0.1.2`, `nimbleterminal.dev/ntpicture v0.1.3-0.20261028191824-58c41671ddab` (pushed main; swap for the tag when released), `github.com/dop251/goja v0.0.0-20261027234916-5f913d1693d9`, `golang.org/x/net v0.62.0`, `github.com/charmbracelet/x/ansi v0.11.9`.
- Every build and test runs with `GOWORK=off CGO_ENABLED=0` (Taskfile env, as in nthtml).
- `jsvm` and `dom` non-test files import none of `os`, `os/exec`, `net`, `net/http`, `syscall`, `io/fs`, `plugin`, `unsafe` (spec §6, enforced by `jsvm.TestNoSystemImports`).
- All Goja compilation goes through `goja.Parse(name, src, sandboxParserOptions()...)` + `goja.CompileAST`; never `goja.Compile`, `MustCompile`, `RunString`, `RunScript` in `jsvm` (spec decision table §6; `jsvm.TestOnlyHardenedCompilePath`).
- Page-controlled text reaches the terminal only through `app.clean` (spec decision 7).
- `nthtml.Model` is created with `nthtml.NewWithOptions(result, viewerOptions)` in `app` (ReflowTypeset off; `StableID` and zlib Kitty frames from the 2026-10-27 performance analysis: a re-flow or scroll step must not repaint the placeholder grid, `app.TestViewerKeepsKittyIDAndSendsZlib`); `document.Options.NoImage` stays false (spec §3, decision 19).
- License MIT, "Copyright (c) 2024-2026 Neomantra Corp"; Taskfile header comment `# ntbrowser - Copyright (c) 2026 Neomantra Corp.` (nthtml convention).
- `GOOS=js GOARCH=wasm go build ./dom ./jsvm ./loop ./netpolicy ./subres` must succeed (spec decision 14: compile-only in v1).
- Commit after every task; conventional-commit subjects (`feat:`, `test:`, `chore:`, `ci:`, `docs:`).
