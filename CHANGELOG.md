# CHANGELOG

## v0.1.0 (unreleased)

 * Initial release of `nimbleterminal.dev/ntbrowser`: a sandboxed, non-JIT terminal web runtime.
 * Packages: `dom` (mutable tree with node, depth, attribute and serialized-size budgets), `jsvm` (hardened Goja sandbox: source maps disabled at parse, time slices and strikes, heap budget, frozen host globals, DOM bindings with browser-like node lifetime, `fetch`), `netpolicy` (the only socket owner: http(s) only, same-origin plus allowlist, resolved-address vetting incl. NAT64/6to4/Teredo, no cookies, bounded bodies), `loop` (single VM goroutine, timers, animation frames, coalesced re-flow through nthtml, ordered page loads), `app` (Bubble Tea host: nthtml viewer with scroll-preserving re-flow, element clicks, navigation modes, history, terminal-boundary sanitization), `cmd/ntbrowser`.
 * Flags: `-nav free|allowlist|confirm`, `-allow`, `-allow-private`, `-external-scripts`, `-width`, `-slice`, `-seed`.
 * Acceptance corpus under `examples/`.
