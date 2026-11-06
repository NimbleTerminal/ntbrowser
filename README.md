# ntbrowser

A secure, non-JIT terminal web runtime: a hardened pure-Go JavaScript interpreter (Goja) drives a mutable DOM that ntbrowser owns, and [nthtml](https://github.com/NimbleTerminal/nthtml) renders it in the terminal.

`ntbrowser` is a terminal reader mode and small-application runtime. It takes an HTML page that may carry JavaScript, runs that JavaScript inside a pure-Go bytecode interpreter with no path to the operating system, lets the script change the page through a narrow proxy API, and shows the result through `nthtml`'s Bubble Tea viewer. The point is a "cognitive shield": the user gets the page's content and legitimate interactivity, and nothing the page does can reach files, sockets, the environment, or the terminal's escape-sequence surface.

## Development dependencies

Use Go 1.26.8 and run `task ci` for the full local and GitHub Actions gate:
formatting, module tidy/verification, vet and tests for both modules, `nocjk`,
WASM compilation, notices, builds, and a 30-second DOM fuzz run. Tasks set
`GOWORK=off CGO_ENABLED=0`. Override the fuzz duration with `task ci DURATION=10s`;
`task check` remains available for checks without builds or fuzzing.

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
| `script-navigation` | off | off | off |
| `subresources` | off | origin | any |
| `fetch` | off | off | allowlist |
| `scripts` | off | off | inline |
| `dynamic-code` | off | off | off |
| `cookies` | off | off | off |
| `session-storage` | off | off | off |
| `files` | off | off | start-page |
| `native-code` | off | off | off |
| `media` | off | image | animation |
| `clipboard` | off | copy | copy |
| enforcement | required | best-effort | best-effort |

`enforcement: required` exits if the operating system cannot enforce the profile; `best-effort` runs and reports those gates as `checked`. On macOS the process re-executes itself under `sandbox-exec`; on Linux it applies landlock and seccomp. `-profile-file path` reads a JSON file with the same fields plus an optional `base`; unknown fields are errors.

No JIT and a coarse script clock make timing attacks harder, not impossible: nothing inside one process is a real boundary against a speculative read of its own memory.

## Flags

Flags apply on top of the chosen profile. Arguments use pflag syntax:
`--name=value` or `--name value`, before or after the URL; `--` ends option
parsing and `-h`/`--help` prints help. Existing single-dash long flags remain
supported.

| Flag | Meaning |
| --- | --- |
| `--profile locked\|reader\|browse` | Built-in profile (default `browse`) |
| `--profile-file path` | JSON profile |
| `--show-profile` | Print the effective profile and exit |
| `--nav off\|free\|allowlist\|confirm` | Link navigation. `off` fetches only the start URL |
| `--allow host[,host]` | Hosts for allowlist gates; a leading dot allows subdomains |
| `--allow-private` | Permit loopback and private addresses (development only) |
| `--subresources any\|origin\|off` | Stylesheets, images and CSS backgrounds |
| `--fetch off\|origin\|allowlist` | `fetch()` from page script |
| `--scripts off\|inline\|external` | Page script (`--external-scripts` is `--scripts external`) |
| `--diagnostics path` | Capture bounded JSONL diagnostics to a new owner-only file |
| `--script-navigation off\|origin\|allowlist\|confirm` | Location navigation, additionally constrained by `--nav` |
| `--script-navigation-limit n` | Automatic navigation chain limit (default 8, maximum 100) |
| `--session-storage off\|on` | Origin-isolated memory-only sessionStorage (default off) |
| `--session-storage-bytes n` | Per-origin key/value byte quota (default 1 MiB; maximum 8 MiB) |
| `--session-storage-total-bytes n` | Total session byte quota (default 8 MiB; maximum 64 MiB) |
| `--cookies off\|session` | In-memory cookies for this browser session (default off) |
| `--dynamic-code off\|on` | Native `eval` and Function-family compilation (default off) |
| `--clipboard off\|copy` | Selected-text copy |
| `--media off\|image\|animation\|video` | The most a top-level response may be shown as. Images inside a page are `--subresources`. `video` also opens `native-code` and `files=temp` |
| `--max-media-bytes n` | Download cap for media (64 MiB) |
| `--width px` | Initial document width; the terminal's pixel geometry takes over once known |
| `--frame-ms n` | Minimum frame interval in ms (default 33, range 1–1000) |
| `--frame-callbacks n` | Pending animation callbacks (default 256, range 1–1024) |
| `--frame-budget-ms n` | Shared callback budget per frame in ms (default 8, range 1–50) |
| `--slice d` | JavaScript time budget per entry (50ms); three overruns disable the page |
| `--seed n` | PRNG seed for `Math.random` (0 = time-based) |

## What a response becomes

A navigation is classified from its headers before its body is read, so a refused kind reads nothing. Pages and text are laid out, images and GIF or WebP animations are shown full view (Esc or Back returns), and anything else stays on the current page with a line such as `application/zip · 2 KB · can't display`.

## Video

With `-media video` and `mpv` installed, activating a link to a video or a focused `<video>` downloads it through the same network policy and plays it: Space pause, ←/→ seek, Esc back. Only the reader can start it; there is no autoplay and no `play()`.

Decoding runs in a separate binary, `ntbrowser-media`, built beside `ntbrowser`. All native code lives there; the main binary links none, and a test proves it. The helper starts before the main process locks itself down and is sandboxed more tightly: no network, no terminal, an empty environment, reading only its libraries and the media directory. It decodes in software with no audio, and sends frames the main process checks and draws. A `<video>` without a known size has no box and cannot be reached this way.

## Keys

`Tab`/`Shift+Tab` move focus through links and form controls · `Enter` opens the focused link or presses the focused button · `Space` toggles a focused checkbox or radio · `⌫` or `⌥←` back · `⌥→` forward · `^R` reload · `/` search · `q` quit. Mouse clicks reach page elements; the page cannot swallow these keys.

**Performance timing.** `performance.now()`, read-only `performance.timeOrigin`,
and `performance.toJSON()` are available. Elapsed time is monotonic with the
default clock. `Date.now()`, performance values and animation-frame timestamps
all use the profile's clock resolution (10 ms by default), with a shared
quantization phase. Injected clocks support deterministic replay and backward
movement is clamped for elapsed time. Legacy `performance.timing`, performance
entries/observers and user-timing marks/measures are not implemented.

**JavaScript anchors.** Parsed and script-created `<a>` elements share an
`HTMLAnchorElement` prototype. The interface exposes `href`, URL components
(`protocol`, `username`, `password`, `host`, `hostname`, `port`, `pathname`,
`search`, `hash`, read-only `origin`), string conversion, `text`, `target`,
`download`, `ping`, `rel`, `hreflang`, `type`, and `referrerPolicy`. URL properties
use the published WHATWG URL parser; relative references use the page URL.
`relList` is a stable, live token-list object with indexed reads, iteration,
`item`, `contains`, `add`, `remove`, `toggle`, `replace`, `forEach`, and `value`.
Its `supports()` returns false because per-link opener/referrer processing is
not implemented. Token validation errors carry the standard error names but
are not `DOMException` instances. `<base>` overrides, obsolete anchor properties,
and script-triggered `.click()` navigation are not implemented.

**Element APIs and security profiles.** A property is data, not permission.
The effect determines which existing gate applies:

| Operation | Boundary |
|---|---|
| Set/read anchor properties or edit `relList` | Local DOM only, under DOM and script budgets; no network or navigation |
| Reader activates a link | Resolve the final destination after click listeners; `navigate` applies off/allowlist/confirm/free |
| Make the admitted navigation request | `netpolicy` checks schemes, credentials, hosts, private addresses, DNS and redirects; content gates apply before consuming the response |
| Set `target`, `download`, `ping`, `rel`, or `referrerPolicy` | Attributes only; no popup, download-to-disk, audit ping or relaxed request policy. Links continue in the current viewer |
| Read `Date`, `performance`, or animation-frame timestamps | The profile’s `clock` resolution; no finer timer through another API |
| Set an image source, attached or detached | `subresources`, with the shared image request and decoding budgets |

URL parsing accepts more schemes than navigation allows. Parsing a `file:`,
`javascript:`, credentialed or private-address URL grants no authority to use it.
Neither JavaScript prototype changes nor attribute writes can change the host's
profile. New host effects must get an explicit gate and denial tests before
being exposed; existing gates are reused according to effect, not element type.

**JavaScript images.** `new Image(width, height)`, `document.createElement("img")`,
and parsed `<img>` elements share an `HTMLImageElement` prototype. Supported:
`src`, `alt`, `width`, `height`, `complete`, `naturalWidth`, `naturalHeight`,
`currentSrc`, `decode()`, and `load`/`error` through `onload`/`onerror` or
`addEventListener`. Detached images load too, through the **subresources** gate
and the same bounded cache as rendered images. Events are asynchronous,
non-bubbling and non-cancelable; replacement sources invalidate old completions.
Failed or superseded decodes reject with an error named `EncodingError`.

This is the basic image lifecycle from the
[HTML standard](https://html.spec.whatwg.org/multipage/embedded-content.html#the-img-element),
not full browser image compatibility. Responsive selection (`srcset`, `sizes`,
`<picture>`), CORS/credential modes, referrer-policy overrides, lazy loading and
fetch-priority hints are not implemented. Dimensions report the explicit HTML
attribute or decoded size, not CSS layout measurements. Decoding uses the
existing bounded still-image decoder (including its SVG sizing rules); failures
are cached for the page, and `EncodingError` is not a `DOMException` instance.
Inline HTML event-handler attributes remain inert. These APIs alone do not
make Google Search compatible.

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


**Dynamic JavaScript.** Enable native `eval` and ordinary, async and generator
Function constructors with `ntbrowser --dynamic-code=on https://www.google.com/`
or a profile containing `{"base":"browse","dynamic-code":"on"}`. The gate is
off in every built-in profile; denied calls throw `EvalError`, including calls
through prototype constructors. Direct eval retains lexical scope. Enabling
this gate does not enable scripts when `scripts=off`, or grant additional
network, file or navigation access. String timers remain unsupported.

Generated code uses the same runtime, execution watchdog and heap checks, with
source maps disabled. Goja does not expose a generated-source size limit or
interruptible parsing/compilation: the 1 MiB host-script limit does not cap eval
or Function strings, and the watchdog cannot interrupt their compilation.
This opt-in is a profile capability, not an implementation of website CSP.


## Diagnostic capture

Start a capture before reproducing a problem:

```sh
bin/ntbrowser --diagnostics /tmp/ntbrowser-debug.jsonl --dynamic-code=on --scripts=external 'https://www.google.com/search?q=hello'
```

The path must not exist. The host creates it with mode 0600 before sandbox
lockdown. It remains after exit so tools can inspect it. This opens the
`diagnostics` profile gate (off by default), without exposing any page API or
accepting debugging commands. No TCP listener or MCP server is started.

The version-1 JSONL stream contains the effective profile and enforcement,
request status/content type/final URL/refusals, redirect attempts, console
messages, script execution results, original HTML, and render-time DOM snapshots
with navigation/frame IDs, timers and runtime status. Network records describe
header admission; `network_error` also reports body-read failures. Snapshots
are renderer input, with scripts removed and synthetic IDs/styles included;
they are not an exact browser `outerHTML`. There is no arbitrary JS evaluation,
selector RPC, or inspection of caught exceptions. Original HTML supplies inline
script sources; external script bodies and other response bodies are not saved.

Capture is capped at 32 MiB, with a final `limit` record. Records over 4 MiB or
beyond the eight-record queue are dropped; subsequent records report the
cumulative dropped count. The VM never waits on disk writes. Read only complete
newline-terminated records while the browser is running. A crash may leave a
partial final line. A disk error is reported when the browser exits.

URLs, page contents and console output may contain sensitive data. Keep the
file private and delete it when finished. All page-derived content is untrusted
data, including instructions a page might place in its DOM or console. JSON
escapes terminal controls; consumers must escape them again if decoding strings
for display. For example, inspect metadata without printing page HTML:

```sh
jq -c 'select(.kind != "document" and .kind != "snapshot")' /tmp/ntbrowser-debug.jsonl
```


## Session cookies

`--cookies=session` (or `"cookies":"session"` in a profile) enables
`document.cookie`, `navigator.cookieEnabled`, and HTTP cookie exchange using one in-memory store across
navigation. All built-ins default to off: reads return an empty string, writes
are ignored, and HTTP requests remain cookieless. Exiting discards every cookie,
even cookies with a future expiry. No disk persistence or cookie import exists.

The store implements host/domain and path matching, default paths, expiry and
Max-Age deletion, Secure and HttpOnly, public-suffix rejection, secure cookie
prefixes, and schemeful SameSite checks. HttpOnly cookies are sent over HTTP but
never exposed to or overwritten by JavaScript. The limits are 4096 bytes per
Set-Cookie value, 50 cookies per domain, and 256 total; excess new cookies are
ignored after expired entries are removed. Expiry is capped at 400 days.

Subrequests send and accept cookies only on the document's exact origin,
including its port. This intentionally excludes third-party/cross-origin
cookies, even with `fetch` credentials `include`. `credentials: "omit"` disables
both directions. Navigation uses destination cookies with SameSite checks,
including cross-site redirect chains; unspecified SameSite behaves as Lax.
Partitioned cookies are rejected. Cookie/Set-Cookie headers remain unavailable
to page JavaScript; the cookie store adds headers only after network admission.
Cookies do not grant navigation, network, storage, or script execution rights.

Cookies, Location and sessionStorage remain a subset of a full browser
challenge environment.


## Location and script navigation

`location` and `document.location` share one Location object. URL getters,
string conversion, `origin`, component setters, `href`, `assign()`, `replace()`
and `reload()` are available. Navigation is off by default and denied calls
throw an error named `SecurityError`. Enable the narrowest needed scope:

```sh
bin/ntbrowser --script-navigation=origin --script-navigation-limit=4 --cookies=session --dynamic-code=on --scripts=external https://www.google.com/
```

- `origin`: only the current HTTP(S) origin, including redirects; `--allow` cannot widen it.
- `allowlist`: the current origin plus `--allow` hosts, including redirects.
- `confirm`: every Location request requires Enter; scripts cannot replace the pending prompt.
- `off`: no Location navigation. URL getters remain available.

The existing `--nav` gate also applies: off blocks all requests; allowlist still
limits confirmed destinations; confirm prompts for cross-origin requests even
when script-navigation is allowlist. Non-HTTP(S) destinations and URL credentials
are always refused. URL inputs are capped at 2048 bytes. No Location path grants
filesystem access, private-network access, or permission to execute JavaScript URLs.

Assignments are coalesced to the last request per VM turn. A document may emit
at most the configured number of requests, and the host limits automatic chains
across documents (default 8, configurable 1–100; JSON zero uses the default).
Reader navigation, Back/Forward, reload, or explicit confirmation resets the
host chain budget. Stale-document and in-flight-load requests are discarded.
Navigation results and denials appear in diagnostic captures.

Assign appends history on success; replace and reload reuse the current entry.
Same-document fragments update the URL and reveal the target without a fetch.
As elsewhere in ntbrowser, Back/Forward refetch the selected history entry.
This is not a complete navigation platform: no frames/cross-window Location,
History API, hashchange/popstate/beforeunload events, or ancestorOrigins list.
The location URL remains the committed document URL while a load or confirmation
is pending. Destination-policy refusals are reported asynchronously by the host.


## Animation frames

`requestAnimationFrame` callbacks run before the upcoming render snapshot.
Each batch shares one profile-coarsened performance timestamp. Cancelling an
unstarted callback works even from another callback in the same batch; newly
requested callbacks wait until the next frame. Navigation discards both queued
and active callbacks.

Frame cadence, pending callback capacity and aggregate callback time are
configurable through `--frame-ms`, `--frame-callbacks`, and `--frame-budget-ms`
(or the same JSON profile keys). Defaults are 33 ms, 256 callbacks, and 8 ms.
Zero selects the default. These are resource controls, not additional script
permissions. Registration at capacity returns 0, as before.

The shared budget shortens each callback's watchdog to the remaining batch
time, without widening its ordinary `--slice` limit. Unstarted callbacks retain
order and are deferred ahead of newly requested callbacks. An interrupted
callback consumes a normal watchdog strike and is not retried. Heap checks and
microtask draining remain part of each callback entry. Go work/GC pauses and
non-interruptible engine operations can overshoot the wall-time target; this
is not a hard real-time deadline or a guaranteed display refresh rate.


## Session storage

Enable with `--session-storage=on` or `"session-storage":"on"` in a profile.
The gate defaults off in every built-in. `window.sessionStorage` returns a
stable `Storage` object for the active document, supporting `length`, `key`,
`getItem`, `setItem`, `removeItem`, `clear`, named-property reads/writes/deletes,
and enumeration. Values are strings; missing getItem/key results are null,
while missing named properties are undefined. Built-in methods remain visible
when a stored key has the same name; use getItem for such keys.

Data survives reloads and navigation back to the same origin within one browser
session. Scheme, normalized host and effective port define the origin; paths
and fragments do not. Separate launches never share data. Only HTTP(S) origins
are supported; disabled or unsupported access throws an error named
`SecurityError`. There is no localStorage, disk persistence, importing, or
cross-origin access. The store has no filesystem or networking capability.

Quotas count two bytes per UTF-16 code unit in both keys and values. Defaults
are 1 MiB per origin and 8 MiB in total, tunable with the byte-quota flags above
(zero selects defaults). Hard caps of 64 nonempty origins and 1024 keys per
origin also apply. Quota failure throws an error named `QuotaExceededError`
and leaves existing values unchanged. Removal and clear release quota; no
origin is silently evicted. Entry order is insertion order; overwriting keeps
position. Method-based access preserves lone UTF-16 surrogates. Named-property
keys follow Goja's string-property conversion, and the dynamic object does not
support arbitrary accessor/symbol own-property descriptors.

No StorageEvent machinery or opener/tab cloning is implemented: ntbrowser has
one active top-level document. Storage contents are not included in diagnostic
snapshots, though page code can explicitly log its own values. Storage errors
currently have the specified names on JavaScript Error objects, not a full
DOMException class hierarchy.

## Dynamically inserted scripts

Classic `<script>` elements created by JavaScript now support `src`, `text`,
`type`, `async`, `defer`, `noModule`, `onload`/`onerror`, and event listeners.
`document.head` is available. Connecting an eligible script (including inside a
subtree), adding its text, or setting its connected `src` prepares it once.
Removing, moving, or changing a prepared script does not execute it again.
External scripts default to asynchronous execution; setting `async=false` before
preparation preserves insertion order among those ordered scripts. Independent
async scripts can finish while the ordered queue is waiting.

External loading requires `--scripts=external` and uses the existing page
fetcher's origin/allowlist, redirect, private-network, cookie, size and timeout
policy. Only successful HTTP responses with a supported JavaScript MIME type
are admitted; HTML and JSON responses never execute as scripts. Fetch failure
or policy refusal dispatches `error`; successful loading dispatches `load`,
while execution errors are reported separately. Events are non-bubbling and
can reach a pending script removed from the DOM. Navigation cancels requests
and discards old-page completions.

Inserted inline source additionally requires `--dynamic-code=on`, preventing
`script.text` from bypassing that gate. It executes synchronously in the
inserting callback's watchdog slice, through the hardened compiler. A blocked
inline script produces a warning. Parser-loaded inline scripts keep their
existing behavior. Dynamic script limits are 32 pending external elements and
256 prepared elements per document; exceeding either throws `TypeError`.
Existing source, DOM, listener and heap budgets still apply.

This is a classic-script subset: modules/import maps remain inert, integrity
metadata is rejected rather than ignored, and reflected `crossOrigin`,
`referrerPolicy` and `nonce` do not grant additional authority. There is no CSP
implementation. Dynamic scripts do not delay `DOMContentLoaded`; `defer` has
no extra scheduling effect on them. `document.currentScript` identifies a
running dynamic script; parser scripts are still extracted from the tree and
report null. Script elements are omitted from rendered HTML.
