# CHANGELOG

## Unreleased

- Handle Ctrl+C before all focused UI and page input, including page search.
  A second press during graphics shutdown quits immediately.

- Confirm heap pressure after garbage collection before disabling scripting,
  avoiding shutdown caused only by transient renderer/navigation allocations.
  Preserve the 256 MiB default and report process-wide bytes and budget.

- Keep SVG-heavy pages readable when nthtml reaches its diagram budget by
  falling back to labels/source under the existing render limits. Add Alt+C
  to copy the complete status/error message through the clipboard gate.

- Add opt-in read-only devtools (`--devtools`, F12) to both commands, with a
  transport-independent versioned inspection API, owner-loop runtime/DOM/policy
  snapshots, and bounded console/network events. Add `-d FILE` for diagnostics.
  Remote listeners, code evaluation and breakpoints remain unavailable.

- Add `glynx`, a browsing-focused executable sharing ntbrowser's engine and
  sandbox startup. Its profile enables public script/frame admission and
  memory-only session state without per-site command-line grants. Add Lynx-style
  navigation and address/search input; retain ntbrowser's existing defaults,
  network vetting and same-origin DOM isolation. Build and package both browser
  executables with the shared media helper.

- Add mandatory macOS signing/notarization and stable Homebrew cask publishing
  alongside verified Linux/macOS release archives containing both browsers and their media helper.

- Add Linux/macOS CI gates and release packaging for amd64/arm64. Archives
  contain both browsers and their media helper and their license notices, with SHA-256 checksums.
  Version tags publish after source checks and archive verification; manual
  dispatch can package an existing tag without moving it.

## v0.1.0 (2026-11-05)

- Initial release of `nimbleterminal.dev/ntbrowser`, a terminal reader and
  non-JIT web runtime using nthtml and a hardened Goja interpreter. Bounded DOM
  mutation, script time slices and heap budgets, timers, animation frames, and
  coalesced reflow support small interactive pages. Source maps are disabled;
  page-controlled terminal text is sanitized.
- Network admission through `netpolicy`: HTTP(S), origin/host gates, DNS-resolved
  address vetting, redirect checks, bounded bodies, and no cookies or environment
  proxy settings. Navigation supports `-nav off|free|allowlist|confirm`, `-allow`,
  and the development-only `-allow-private` override.
- Stylesheets, CSS `@import`, raster/SVG images, and CSS backgrounds through
  `subres`, controlled by `-subresources`. Mixed-content blocking and byte,
  pixel, rule, request, and concurrency budgets bound subresource loading.
- Interactive forms: ordered keyboard focus, text and textarea editors, select
  choosers, and checkbox/radio controls. GET submission uses the navigation
  policy; POST submission and filled-in password fields are refused.
- Frozen `locked`, `reader`, and `browse` profiles via `profile` and `sandbox`:
  `-profile`, `-profile-file`, and `-show-profile` expose policy and effective
  enforcement. macOS uses sandbox-exec; Linux uses Landlock and seccomp.
  Required enforcement fails when unavailable; best-effort reports checked gates.
- Top-level HTML, text, images, and GIF/WebP animation routing through `content`.
  `-scripts`, `-fetch`, `-clipboard`, `-media`, and `-max-media-bytes` control the
  relevant capabilities. `-external-scripts` remains an alias for
  `-scripts external`; `-width`, `-slice`, and `-seed` configure rendering and
  script execution.
- Opt-in, click-to-play video with `-media video`: the separate
  `cmd/ntbrowser-media` helper uses ntconfine, ntmediabox, and ntvideo for
  OS-confined software decoding without audio. The host links no native code,
  starts the helper before lockdown, enforces the media byte budget, rejects
  stale frames after seeks or replacement, and terminates and reaps the helper.
- Scroll-preserving reflow, link activation, navigation history, and stable
  Kitty image IDs with compressed frames. CJK fallback fonts are included via
  `ntfonts/cjk`; build with `-tags nocjk` to omit them.
- Published dependency pins, including nthtml v0.2.3 and nttypeset v0.1.9,
  require no sibling library checkouts. Acceptance examples, native tests,
  a JS/WASM compile gate, and generated third-party notices cover the build.
- Dual licensing under
  `AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial`;
  see `LICENSE` and `LICENSE-COMMERCIAL.md` for the terms.
