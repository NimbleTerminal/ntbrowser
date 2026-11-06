# ntbrowser

A secure, non-JIT terminal web runtime: a hardened pure-Go JavaScript interpreter (Goja) drives a mutable DOM that ntbrowser owns, and [nthtml](https://github.com/NimbleTerminal/nthtml) renders it in the terminal.

`ntbrowser` is a terminal reader mode and small-application runtime. It takes an HTML page that may carry JavaScript, runs that JavaScript inside a pure-Go bytecode interpreter with no path to the operating system, lets the script change the page through a narrow proxy API, and shows the result through `nthtml`'s Bubble Tea viewer. The point is a "cognitive shield": the user gets the page's content and legitimate interactivity, and nothing the page does can reach files, sockets, the environment, or the terminal's escape-sequence surface.

## Development dependencies

Build with Go 1.26.8 and run `task check build build-nocjk`. Tasks set
`GOWORK=off CGO_ENABLED=0`.

Dependencies use published module versions, including nthtml v0.5.0 and
nttypeset v0.3.0. No sibling library checkouts are required. The media helper's
replacement of ntbrowser points to this repository's root module.
ntbrowser retains the WASM-capable Bubble Tea fork for its compile-only gate.
Run `task notices` after dependency changes.

Media workers use ntconfine for OS confinement, ntmediabox for the streaming
protocol, and ntvideo's shared worker decoder inside `ntbrowser-media`.
The host rejects queued frames/statuses from before a seek or media replacement
and passes the profile's file budget to the decoder. The worker also applies
ntvideo's default source-dimension limits. The opt-in real playback check is
`NTVIDEO_TEST_FILE=/absolute/path/clip.mp4 task test`; it requires libmpv and OS
confinement and checks file-limit rejection, recovery, and repeated playback.

## Chunked reading

For long documents, opt into semantic reading mode:

```sh
ntbrowser -chunked https://example.com/long-document
```

`-chunked` uses nthtml's HTML-to-nttypeset adapter. It has reduced CSS support:
CSS background images are omitted, ancestor decoration repeats across chunks,
and layout follows semantic reading order. Default CSS layout semantics are unchanged.

Use `[` / `]` for previous/next chunk and `Ctrl+Home` / `Ctrl+End` for first/last.
In raster mode, scrolling again at a chunk's top or bottom also crosses to its
neighbor. Explicit chunk keys work in TTY mode too. The help line shows the
current chunk. Local fragments and script focus resolve through the chunk index;
Tab follows the focusable elements represented in the displayed chunk.
Search, selection, and copying are local to that chunk and reset on a chunk
change. TTY uses a plain semantic representation; source/hit geometry is at the
adapter's block/section granularity rather than the CSS renderer's inline boxes.

Parsing and DOM limits still apply to the whole page. Extraction is lazy, with
at most two cached layouts/source maps; each semantic unit is bounded to 256
HTML nodes and 16 KiB of source text/attributes, including ancestor context.
Layout/raster work stays on the render worker under the two-second frame deadline;
rasters retain the existing pixel/height ceilings. An indivisible unit that
exceeds a budget reports an error without truncation. Use the chunk keys to skip
it; the last readable image remains visible until another chunk succeeds.
DOM, stylesheet, image, and width changes rebuild the index, preserving a stable
source anchor when available. No new profile gates are opened.

The default CSS renderer uses nthtml v0.5.0 viewport painting: full bounded CSS
layout, with only a bounded raster band resident. A serial worker paints the
first band with each layout and handles later scroll/zoom requests with a
two-second deadline. Search, selection, focus and hit-testing retain global CSS
coordinates. While painting is pending, the previous band stays visible; input
against an outdated DOM is rejected. DOM/layout limits still apply, and TTY uses
the whole text layout. See [the viewport design](docs/design/2026-11-05-ntbrowser-viewport-rendering-design.md).
It requires a future nthtml viewer API; the `-chunked` flag does not enable it.

## Release binaries

GitHub releases provide `tar.gz` archives for Linux and macOS on amd64 and
arm64, plus `checksums.txt` (SHA-256). Install **both** `ntbrowser` and
`ntbrowser-media` from the same archive into the same directory. The main
executable also serves as the Linux confinement stage. Video playback needs
libmpv installed separately; the libraries are not bundled. Release macOS
binaries are signed and notarized; local/CI snapshots are unsigned.

Stable releases also publish a macOS cask to `NimbleTerminal/homebrew-tap`:

```sh
brew install --cask nimbleterminal/tap/ntbrowser
brew install mpv  # optional, for video playback
```

The cask installs both executables. Only the media helper has the
`disable-library-validation` entitlement needed to load third-party libmpv;
the main browser receives no additional hardened-runtime entitlements.

CI runs checks and builds on Linux and macOS, including the nested media module,
the WASM compile gate, no-CJK builds, formatting, module tidiness, and notices.
The Release workflow packages and verifies all four target archives on pull
requests and pushes to `main`, without publishing. `v*` tags trigger checks of
the exact source commit before publishing binaries and the tagged changelog's
release notes. Only the publishing job has repository write permission.

For an existing tag such as `v0.1.0`, run **Actions → Release → Run workflow**
from `main` and supply the tag. Automation comes from the selected workflow
commit; application source and release notes come from the existing tag. This
does not move tags or overwrite existing releases. An existing partial release
must be inspected and resolved before retrying publication.

Locally, install GoReleaser v2.16.0, actionlint, and Python 3.11+; run
`task release-check release` to validate the workflow and create verified
snapshot archives in `dist/`. Nothing is published by those tasks.

Release maintainers must configure these GitHub Actions secrets (repository
secrets or organization secrets granting this repository access):

- `MACOS_SIGN_P12`: base64 Developer ID Application certificate and private key.
- `MACOS_SIGN_PASSWORD`: certificate export password.
- `MACOS_NOTARY_ISSUER_ID`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_KEY`:
  App Store Connect notarization API credentials, with the key base64 encoded.
- `HOMEBREW_TAP_GITHUB_TOKEN`: token with contents write access to
  `NimbleTerminal/homebrew-tap`.

Signing and notarization are mandatory for releases. Both macOS binaries must
finish notarization before archives and checksums are generated. The workflow
validates archives before GitHub publication, then updates the stable cask using
those archive hashes. Prereleases do not change the tap. If only tap publication
fails, download and verify the published archives, generate the cask with
`python3 scripts/release.py cask DIST TAG`, then retry
`GH_TOKEN=... python3 scripts/publish_cask.py CASK TAG` with the tap token.
Cask updates use GitHub's existing-file SHA check and never force-push.

## Usage

```
ntbrowser [-profile locked|reader|browse] [-profile-file path] [-show-profile] [gate flags] [-width px] [-slice 50ms] [-seed n] URL
```

`URL` is `http(s)://…` or, for the start page only, `file://…`.

## Profiles

A profile is a named set of positions for every gate out of the sandbox. It is fixed at launch, passed by value, and invisible to the page. The zero profile has every gate closed; a profile only opens gates. `-show-profile` prints the effective profile, with how each gate is held on this machine: `compiled-out` (the code is not in the binary), `os` (the kernel refuses) or `checked` (our code refuses).

| | `locked` | `reader` | `browse` (default) |
| --- | --- | --- | --- |
| `navigate` | off | confirm | free |
| `subresources` | off | origin | any |
| `fetch` | off | off | allowlist |
| `scripts` | off | off | inline |
| `files` | off | off | start-page |
| `native-code` | off | off | off |
| `media` | off | image | animation |
| `clipboard` | off | copy | copy |
| enforcement | required | best-effort | best-effort |

`enforcement: required` exits if the operating system cannot enforce the profile; `best-effort` runs and reports those gates as `checked`. On macOS the process re-executes itself under `sandbox-exec`; on Linux it applies landlock and seccomp. `-profile-file path` reads a JSON file with the same fields plus an optional `base`; unknown fields are errors.

No JIT and a coarse script clock make timing attacks harder, not impossible: nothing inside one process is a real boundary against a speculative read of its own memory.

## Flags

Flags apply on top of the chosen profile.

| Flag | Meaning |
| --- | --- |
| `-profile locked\|reader\|browse` | Built-in profile (default `browse`) |
| `-profile-file path` | JSON profile |
| `-show-profile` | Print the effective profile and exit |
| `-nav off\|free\|allowlist\|confirm` | Link navigation. `off` fetches only the start URL |
| `-allow host[,host]` | Hosts for allowlist gates; a leading dot allows subdomains |
| `-allow-private` | Permit loopback and private addresses (development only) |
| `-subresources any\|origin\|off` | Stylesheets, images and CSS backgrounds |
| `-fetch off\|origin\|allowlist` | `fetch()` from page script |
| `-scripts off\|inline\|external` | Page script (`-external-scripts` is `-scripts external`) |
| `-clipboard off\|copy` | Selected-text copy |
| `-media off\|image\|animation\|video` | The most a top-level response may be shown as. Images inside a page are `-subresources`. `video` also opens `native-code` and `files=temp` |
| `-max-media-bytes n` | Download cap for media (64 MiB) |
| `-width px` | Initial document width; the terminal's pixel geometry takes over once known |
| `-slice d` | JavaScript time budget per entry (50ms); three overruns disable the page |
| `-seed n` | PRNG seed for `Math.random` (0 = time-based) |

## What a response becomes

A navigation is classified from its headers before its body is read, so a refused kind reads nothing. Pages and text are laid out, images and GIF or WebP animations are shown full view (Esc or Back returns), and anything else stays on the current page with a line such as `application/zip · 2 KB · can't display`.

## Video

With `-media video` and `mpv` installed, activating a link to a video or a focused `<video>` downloads it through the same network policy and plays it: Space pause, ←/→ seek, Esc back. Only the reader can start it; there is no autoplay and no `play()`.

Decoding runs in a separate binary, `ntbrowser-media`, built beside `ntbrowser`. All native code lives there; the main binary links none, and a test proves it. The helper starts before the main process locks itself down and is sandboxed more tightly: no network, no terminal, an empty environment, reading only its libraries and the media directory. It decodes in software with no audio, and sends frames the main process checks and draws. A `<video>` without a known size has no box and cannot be reached this way.

## Keys

`Tab`/`Shift+Tab` move focus through links and form controls · `Enter` opens the focused link or presses the focused button · `Space` toggles a focused checkbox or radio · `⌫` or `⌥←` back · `⌥→` forward · `^R` reload · `/` search · `q` quit. Mouse clicks reach page elements; the page cannot swallow these keys.

**Forms.** Moving focus to a text field (Tab or a click), or `Enter` on it, opens an editor in the status line (a 6-row panel for a textarea); the page shows the value as you type. In the editor, `Enter` submits the form, `Tab`/`Shift+Tab` move on, `Esc` returns to browsing with focus kept, and `^C` still quits; `q` and `/` are text. A page's script can move focus but never starts typing for you. `Enter` on a select opens a chooser (`↑`/`↓`, `Enter` to set, `Space` toggles in a multiple select, `Esc` cancels). GET forms submit through the same navigation gate as links (`-nav confirm` asks "Submit form to <host>?"). POST forms, and GET forms with a filled-in password field, are not submitted; the status line says why.

## Security model

JavaScript runs in a pure-Go interpreter with no `os`, `net` or environment access, a per-entry time slice, a heap budget, and a bounded DOM; the only network path is a `fetch()` confined to the page's origin plus `-allow`, dialled through an address-vetting transport. Page text reaches the terminal only through a control-stripping boundary.

| Threat | Control | Enforced in |
| --- | --- | --- |
| Script reads/writes files | no `os` import; source maps disabled at the `Parse` call (not just on the runtime); `file:` rejected by `Allowed` and `ResolveURL` | `jsvm` (three tests), `netpolicy` |
| Script opens sockets / SSRF to metadata or LAN | only `fetch` exists; scheme/host allowlist; DNS-resolved IP vetted then dialed (`netip`-based: loopback, RFC1918, link-local, CGNAT, multicast, plus NAT64 `64:ff9b::/96` by embedded address, `64:ff9b:1::/48`, 6to4 by embedded address, Teredo, IPv4-compatible); redirects re-admitted; no proxy env; top-level navigation uses the same dialer with `AnyOrigin` | `netpolicy` (`TestForbiddenIPCoversTransitionPrefixes`) |
| Subresource SSRF (`<link>`, `<img>`, `@import`, `url()` to metadata or LAN) | the same dialer and address vetting as `fetch()`; loopback and private targets only with `-allow-private`; redirects re-admitted; an `https:` page refuses `http:` subresources | `netpolicy` (`TestKindStyleAcceptAndGate`, `TestNoDowngradeFromHTTPS`), `loop` (`TestMixedContent`) |
| Soft-404 or interstitial injected as CSS | stylesheets need a 2xx `text/css` (or octet-stream with a `.css` path); images need a 2xx decodable type | `netpolicy` (`TestKindStyleAcceptAndGate`, `TestKindImageAcceptAndGate`) |
| Pixel bombs and request floods | `DecodeImage` reads the header first and caps 1 MiB / 4096 px / 4 Mpx; 16 Mpx per page; 64 images and 16 stylesheets per page; six in flight | `subres` (`TestImageCapsAndPixelBudget`, `TestMaxInFlightQueue`) |
| A stylesheet that never arrives holds the first frame | the first frame waits at most 1.5 s for the blocking stylesheets, then paints unstyled and restyles when they land | `loop` (`TestStyleTimeoutUnstyledThenStyled`) |
| Exfiltrate via DNS/timing | no DNS primitive; 10 ms clock; MaxInFlight 4 | `jsvm`, `netpolicy` |
| Terminal escape injection via `console.log`, `<title>`, hrefs, warnings or error strings | `jsvm.safeString` for console text; `app.clean` (C0, DEL, C1, U+2028/9) on everything stored for the status and title lines and again in `View`; nthtml strips controls on its own paths | `jsvm`, `app` (`TestViewNeverCarriesTerminalEscapes`) |
| CPU exhaustion | per-entry slice, strikes → disabled, call-stack cap, interval clamp | `jsvm`, `loop` |
| Memory exhaustion | `dom.Limits` incl. `TotalBytes` ≤ nthtml limits (serialized size bounded, so `Render` cannot fail on a within-budget tree); listener/timer/frame caps; body and script caps; heap watchdog → `dropPage` (runtime closed, requests cancelled, GC); `SetMemoryLimit` as a soft last layer | `dom`, `jsvm`, `loop`, `netpolicy`, `cmd` |
| UI ↔ VM deadlock | VM sends via buffered outbox + forwarder; UI never blocking-posts | `loop` |
| Node/listener budget exhaustion via detached or never-inserted nodes | every parentless node is released when no wrapper is alive (GC cleanup reports); forced GC + bounded wait + one retry under pressure; transactional writes; counts kept as integers in Go | `dom`, `jsvm` (tests) |
| Stale navigation wins (UI URL ≠ VM page) | `LoadPage` generation; `pageLoadedMsg` sequence; history moves on success only | `loop`, `app` (tests) |
| Navigation blocked by the fetch allowlist, or fetch widened by navigation | two policies: `AnyOrigin` only for top-level navigation under free/confirmed, never for `fetch()`; both share the IP-vetting dialer | `netpolicy`, `app` |
| Page keeps the terminal busy with clicks the user did not make | clicks and keys originate only from Bubble Tea input; the page has no synthetic `dispatchEvent`/`click()` | `loop` |
| Prototype pollution of host paths | captured `JSON.parse`; frozen globals; wrappers resolve through `Tree` each call | `jsvm` |
| Use-after-free / handle forgery | integer `NodeID`s, never reused; forged ids reach only this page's own nodes | `dom`, `jsvm` |
| Page hijacks `q`/`ctrl+c`, search typing, or navigation | host handles keys first and withholds search-box keystrokes; navigation is user-initiated only, resolved against base, http(s) only after the start page; JS can only `preventDefault` | `app`, `loop` |
| Page captures keystrokes or leaks typed form data | only a reader action (Tab, click, Enter) starts text entry, never script `focus()`/`autofocus`; typed values leave only through a submission the reader triggered, through the link gate; GET with a password and POST are refused; values are never logged | `app`, `loop` |
| Frame storm | dirty flag + coalescing + `MinFrame`; one render in flight; stale frames dropped by `Gen` | `loop`, `app` |

## Architecture

```
                    Bubble Tea goroutine                         VM goroutine (loop.Run)                 workers
  ┌──────────────────────────────────────────┐      ┌─────────────────────────────────────────┐   ┌──────────────────┐
  │ app.Model.Update                         │      │ inbox chan func()  (256, drop-on-full   │   │ netpolicy.Fetcher│
  │  tea.KeyPressMsg ──► loop.Input ─────────┼─────►│   for input; blocking for completions)  │◄──┤  .Do()  (HTTP)   │
  │  nthtml.LinkMsg  ──► loop.LinkActivated ─┼─────►│ timers heap ─► Sandbox.Call (watchdog)  │   └──────────────────┘
  │  WindowSize      ──► loop.Resize ────────┼─────►│ Sandbox.Dispatch(Event) ─► JS listeners │   ┌──────────────────┐
  │                                          │      │ dom.Tree (mutable, single owner)        │   │ document.Render  │
  │  loop.FrameMsg ──► viewer = nthtml.New() │◄─────┤ dirty? ─► coalesce ≥ MinFrame ─► ───────┼──►│  (immutable HTML │
  │  loop.NavigateMsg ─► app.Navigate (policy)│◄────┤ Serialize() snapshot                    │   │   snapshot)      │
  │  loop.LogMsg ───► status line (sanitized)│◄─────┤ FrameMsg{Gen, *document.Result}  ◄──────┼───┤                  │
  └──────────────────────────────────────────┘      └─────────────────────────────────────────┘   └──────────────────┘
```

Design: [docs/design/2026-10-25-ntbrowser-architecture-design.md](docs/design/2026-10-25-ntbrowser-architecture-design.md).

## License

Copyright (c) 2026 Neomantra Corp. All rights reserved.

`ntbrowser` is dual-licensed (`AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial`):

1. **Open-Source & Local CLI/Desktop Use:** Licensed under the **GNU Affero General Public License v3.0 only (`AGPL-3.0-only`)** with the **NimbleTerminal Open-Source Linking Exception (AGPLv3 Section 7)** in [`LICENSE`](LICENSE), allowing MIT-licensed modules from the `NimbleTerminal`, `NimbleMarkets`, and `wethinkt` organizations (including `ntvideo`, `ntpicture`, `ntpdf`, `ntsvg`, `nttypeset`, `gloss`, and `go-thinkt`) to import and link `ntbrowser` directly for local CLI and desktop execution while remaining 100% MIT-licensed.
2. **Cloud, Hosted Sandbox & Closed-Source OEM Use:** Deploying, hosting, streaming, or executing `ntbrowser` (or tools powered by it) inside a commercial hosted service, remote terminal/multiplexer service, cloud AI agent sandbox, multi-tenant server, or closed-source commercial product requires either full **AGPLv3 Section 13** Corresponding Source disclosure of the covered work or a **Commercial OEM & Enterprise License from Neomantra Corp.** (see [`LICENSE-COMMERCIAL.md`](LICENSE-COMMERCIAL.md) or contact `licensing@neomantra.net`).
