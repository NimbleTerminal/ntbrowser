# CHANGELOG

## Unreleased

- Define `crypto.getRandomValues` and `crypto.randomUUID` in the script
  sandbox, so Go's `wasm_exec.js` loader and other scripts that require
  `globalThis.crypto` no longer abort with an uncaught error (seen on gloss's
  web terminal frame). Both draw from the sandbox's host-replaceable random
  source behind `Math.random`, per design decision 10, so replays stay
  deterministic and reader-mode scripts still get no CSPRNG. Integer typed
  arrays only, 65,536 bytes per call, in-place fills of the view's own bytes;
  `crypto.subtle` stays absent for honest feature detection.

- Report a classic script that reaches for a dynamic `import()` (SvelteKit and
  Vite bootstraps, for example) as a skipped unsupported feature, in a `warn`
  status line naming the script, instead of a `SyntaxError` blaming the page.
  Goja has no ES module loader; `type="module"` scripts were already skipped
  by type. Genuine syntax errors keep their reports and the script budgets are
  unchanged.

- Probe the terminal's Kitty graphics support at program start, through one
  terminal session shared by every page viewer and media picture, so discovery
  finishes while the first page loads. In a terminal that identifies itself as
  Kitty-capable (rex, kitty, ghostty, WezTerm, …) the first frame waits for the
  reply, bounded by ntpicture's 250 ms probe timeout, instead of painting
  glyphs and switching a moment later. Unidentified terminals still paint at
  once. The session closes on shutdown after the pictures.

- Render GitHub's repository sidebar beside the README and keep narrow page
  layouts readable, using the local nthtml renderer's fixed-width intrinsic
  sizing, 4,096 custom properties per element, `calc()` width media ranges,
  column flex minimum heights and clipped alt text in sized image boxes. No
  ntbrowser budget or gate changes.

- Apply CSS `:hover` in the default CSS viewer using presented-frame hit tests
  and generation checks. Coalesce worker renders under the existing frame
  pacing and deadline; pages without hover rules avoid pointer-driven reflows.

- Enable full pointer-motion reporting for viewer link hover/press feedback.
  The local nthtml renderer supplies cyan hover and amber pressed outlines
  without changing navigation permissions or DOM/frame dispatch gates.

- Admit up to 32,768 CSS rules in glynx now that the local nthtml renderer
  evaluates cascade-layer blocks. Keep ntbrowser's 4,096-rule default and
  existing selector-work, stylesheet-byte and script-mutable DOM limits.

- Give glynx bounded host-only stylesheet storage (4 MiB), up to 64 sheets and
  1 MiB per sheet plus 16,384 rules, configurable with stylesheet flags. Keep script-mutable DOM
  limits and ntbrowser defaults unchanged; match the renderer byte ceiling to
  the combined allowance and retain network/selector gates.

- Give glynx a 512 MiB process-heap budget while retaining ntbrowser's 256 MiB
  default. Add `--heap-budget-mib` (64–2048) and record the effective budget in
  diagnostics; the post-GC guard and all resource/authority gates remain active.

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
