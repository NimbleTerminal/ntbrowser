# ntbrowser

A secure, non-JIT terminal web runtime: a hardened pure-Go JavaScript interpreter (Goja) drives a mutable DOM that ntbrowser owns, and [nthtml](https://github.com/NimbleTerminal/nthtml) renders it in the terminal.

`ntbrowser` is a terminal reader mode and small-application runtime. It takes an HTML page that may carry JavaScript, runs that JavaScript inside a pure-Go bytecode interpreter with no path to the operating system, lets the script change the page through a narrow proxy API, and shows the result through `nthtml`'s Bubble Tea viewer. The point is a "cognitive shield": the user gets the page's content and legitimate interactivity, and nothing the page does can reach files, sockets, the environment, or the terminal's escape-sequence surface.

## Two applications, one engine

`ntbrowser` launches a page under a controlled sandbox policy. **glynx** is the
browsing-oriented application: it grew out of needing a long command-line
incantation just to visit Google. Both share the DOM, JavaScript runtime,
networking, renderer and confinement code; compatibility fixes benefit both.

| | ntbrowser | glynx |
|---|---|---|
| Purpose | Launch a page under an explicit security profile | Browse from site to site |
| Default profile | `browse`: inline scripts, explicit extra permissions | `glynx`: external scripts, dynamic code, frames and session state enabled |
| Interface | Runtime and policy controls | Lynx-style navigation, address/search input |
| State today | Explicit, limited session features | Memory-only cookies, session storage and navigation history |
| Persistence | No automatic persistence | Bookmarks and persistent browsing state are future work |
| Implementation | Shared browser engine | Separate executable using the same engine |

Build both with `task build`, then run:

```sh
bin/glynx https://www.google.com
bin/glynx                         # opens address/search input
bin/glynx "hello world"           # Google search
bin/glynx --show-profile          # inspect effective permissions
bin/glynx --profile reader        # replace browsing defaults with reader policy
```

In glynx, `g` or `Ctrl+L` opens address/search input. Use ↑/↓ to move focus,
→/Enter to open, ← to go back, `f` to go forward, Page Up/Down to scroll,
`/` to search the page, `Ctrl+R` to reload, and `q` to quit. Editing fields,
search input and focused child-frame scrolling keep their own key handling.
Addresses can omit `https://`; plain words or phrases search Google.
The name and keymap recall the [historical graphical Lynx proposal](https://wearcam.org/glynx.htm).

Glynx enables `scripts=external`, `script-sources=any`, `dynamic-code=on`,
`subresources=any`, `frames=any`, `frame-dom=same-origin`,
`cookies=session`, `session-storage=on`, `navigate=free`, and
`script-navigation=confirm`. Script and frame admission includes public HTTP(S)
origins and redirects without per-site grants. HTTPS subrequests cannot downgrade
to HTTP. Script-request MIME/SRI checks, private-network restrictions, same-origin
DOM isolation, watchdogs, body limits and host confinement still apply.
Script fetch remains origin-only; broad script/frame admission does not grant
cross-origin fetch response access. Script-triggered navigation asks first.
Child-frame cookies and storage remain disabled.

All policy flags are available in both commands. For example,
`--script-sources=grants` restores explicit cross-origin script grants and
`--frames=origin` confines child admission to the page origin.
`--profile` and `--profile-file` replace the executable's defaults; they never
silently inherit glynx permissions. Existing ntbrowser defaults are unchanged.
The zero profile remains closed.

This is a supported `cmd/glynx` application in this repository, not a fork or
a separate browser engine. Startup and sandbox re-exec live in
`internal/browsercmd`. More permissive defaults remove setup friction; they do
not supply missing browser APIs or guarantee that Google/reCAPTCHA accepts the
browser. Persistent bookmarks, cookies, and downloads are not added by this
initial separation.

Status and error messages can be copied with **Alt+C** in either application.
This copies the full message, including text clipped off-screen, using the
existing `clipboard=copy` permission and the terminal's OSC 52 support. It does
not copy editor contents or change the current message.

## Read-only devtools

Both applications offer an opt-in inspector:

```sh
glynx --devtools https://www.google.com
ntbrowser --devtools https://example.com
glynx --devtools -d /tmp/glynx-debug-1.jsonl https://www.google.com
```

Press **F12** to open or close the panel. Tab switches between overview/permissions,
elements, and console/network. Use ↑/↓ or Page Up/Down to scroll and `r` to refresh.
In Elements, `1`–`9` opens a child, Backspace returns to the document root,
`n` gets the next page, and `f` selects the next frame. In Events, `n` advances
to the next batch. Esc closes the panel; Ctrl+C quits. Refresh the overview
after navigation to acquire the new document's inspection handles.

Devtools is off in every built-in profile, including glynx. Enable it with
`--devtools` or `"devtools": true` in a profile file. It needs no diagnostic
file. `-d FILE` is now shorthand for `--diagnostics FILE`; file capture still
requires a new owner-only file and retains its existing full diagnostic scope.

The panel uses the versioned `devtools.Session.Do(context, Request)` API.
Capabilities are `snapshot`, `dom`, and `events`; the same copied request/response
types can be JSON-encoded by a future authenticated IPC or remote adapter.
No remote listener, IPC server, evaluation, editing, page controls, or breakpoint
support is enabled in this first version. Session IDs identify a target; they
are not authentication credentials.

Inspection runs on the browser owner loop and never evaluates JS, getters or
custom string conversions. DOM requests require the session, frame and unique
document revision, and stale handles are rejected. Results are paginated and
bounded; recent events use a 256-entry ring with loss reporting and two-second
request deadlines. Network metadata excludes bodies/headers, credentials and
URL query/fragment data. DOM inspection omits form values, arbitrary attributes,
and script/style/textarea text. Console messages, DOM text and inline styles can
still contain page data: inspection access is privileged host access.

The live panel and JSONL recorder share observation hooks, while applying their
own data policies. Cookies and storage contents are not exposed by devtools.

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

## Pages with many SVG icons

If nthtml's aggregate diagram count or pixel budget is reached, the browser
retries using SVG labels and Mermaid source text. CSS, ordinary images and
navigation remain available. This fallback stays active for the current page
and resets on navigation; a warning identifies the reduced diagram rendering.
The original render deadline and resource limits still apply. Other resource
limit failures are not silently ignored.

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
ntbrowser [-profile locked|reader|browse] [-profile-file path] [-show-profile] [gate flags] [-width px] [-slice 50ms] [-seed n] [URL]
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
| `--allow host[,host]` | Fetch/navigation allowlist hosts; a leading dot allows subdomains; scripts use `--allow-script` |
| `--allow-private` | Permit loopback and private addresses (development only) |
| `--subresources any\|origin\|off` | Stylesheets, images and CSS backgrounds |
| `--fetch off\|origin\|allowlist` | `fetch()` from page script |
| `--scripts off\|inline\|external` | Page script (`--external-scripts` is `--scripts external`) |
| `--allow-script SOURCE_ORIGIN=DESTINATION_ORIGIN` | Repeatable exact-origin script grant, independent of fetch/navigation |
| `--script-prompts` | Ask for one-request or session script grants; default off |
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
| `--frame-dom off/same-origin` | Bounded same-origin DOM sharing between enabled frames (default off) |
| `--frames MODE` | Isolated iframe documents (default off) |
| `--allow-frame SOURCE=DEST` | Repeatable frame origin pair, used with `--frames=allowlist` |
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

Ctrl+C exits from every focus mode, including page search, address entry,
forms, frames, and devtools. Press it again during shutdown to quit immediately
if graphics cleanup is waiting.

The heap guard defaults to 512 MiB in glynx and 256 MiB in ntbrowser, measured
as **process-wide Go heap**, including the renderer. Set `--heap-budget-mib=768`
to override it (64–2048 MiB). It collects garbage and rechecks before disabling scripting, then
reports the measured bytes and budget. This is not a per-script memory meter;
the last rendered page remains available when scripting is disabled.


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

Promise rejections are reported after the current JavaScript entry and its
microtasks finish. A handler attached during that turn suppresses the report;
a handler attached in a later turn does not retract an earlier report. Tracking
is limited to 256 pending rejections per turn, with one warning if tracking
overflows. Handled entries immediately release their slots.

Unhandled promise-rejection logs include up to 12 captured JavaScript frames
(function, source URL, line, and column), within the existing 4 KiB log bound.
Native Error values retain their creation stack; other rejection values use
the rejection site when available. The logger does not read page-controlled
`stack` properties. These details appear in `console` diagnostic records when
`--diagnostics FILE` is enabled; the terminal may truncate the status line.

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
This is not a complete navigation platform: no cross-window Location,
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

External loading requires `--scripts=external` and a same-origin destination
or a source-scoped script grant. It retains the page transport’s redirect,
private-network, cookie, size and timeout policy and shared in-flight budget. Only successful HTTP responses with a supported JavaScript MIME type
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

This is a classic-script subset: modules/import maps remain inert. Integrity
metadata is verified as described below; reflected `referrerPolicy` and `nonce`
do not grant additional authority. There is no CSP
implementation. Dynamic scripts do not delay `DOMContentLoaded`; `defer` has
no extra scheduling effect on them. `document.currentScript` identifies a
running dynamic script; parser-script execution still reports null. Parsed
script nodes remain discoverable but never replay through insertion or mutation.
All script elements are omitted from rendered HTML.

DOM queries support attribute-presence selectors such as `[nonce]`,
`script[nonce]`, `.item[data-ready]`, and descendant combinations. Empty-valued
attributes count as present; HTML attribute names are case-insensitive. Queries
are bounded to 4096 bytes, eight descendant parts and eight presence tests per
part. Attribute value operators, namespaces, escapes and general compound CSS
selectors remain unsupported. This follows the presence-selector semantics in
[Selectors Level 4](https://www.w3.org/TR/selectors-4/#attribute-representation).

`document.getElementsByTagName()` and `element.getElementsByTagName()` return
live `HTMLCollection` objects: `length`, indexed access, `item()`, `namedItem()`,
and iteration reflect current descendants in document order. `*` matches all
elements; an element query excludes itself. Collections retain detached roots
while reachable, and lookups stay within the existing bounded DOM. This bridge
uses HTML case-insensitive tag matching; namespace-sensitive SVG/XML matching
and arbitrary collection expandos are not implemented. Parsed scripts (including
data blocks) now count against DOM node/text budgets and are available for nonce
lookup and insertion anchors. Their execution plan remains separate from the
rendered tree.

### Script integrity verification

Parsed and dynamically inserted external scripts support SHA-256, SHA-384 and
SHA-512 `integrity` digests. Only the strongest supplied supported algorithm is
used; any matching digest of that strength admits the response. The exact
bounded, decompressed response bytes are verified before compilation. A mismatch
never reaches the VM. Dynamic scripts report failure through `error`, and an
ordered queue continues past the failed entry. Parser-script failures are logged.

Cross-origin integrity loads additionally require a `crossorigin` attribute
(`anonymous` or its equivalent empty/invalid value) and exactly one matching
`Access-Control-Allow-Origin` header, or `*`. The host sends the document's
serialized Origin; page-provided request headers cannot replace it. Existing
script-origin grants, private-network, redirect, MIME, cookie, body and timeout gates
still apply. Integrity grants no permission to fetch an otherwise denied host.

This implementation fails closed for credentialed integrity loads, redirects
that change origin, malformed recognized hashes, metadata containing no supported
hash, and integrity options. Same-origin redirects are supported, with CORS
checked on redirect responses when the resource is cross-origin to the page.
Metadata is bounded to 4096 bytes and 32 tokens. Unsupported algorithms can
coexist with a valid supported hash; an empty integrity attribute adds no check.
These stricter failure rules are intentional differences from a full browser.
CSP, Integrity-Policy headers, stylesheet integrity and general fetch CORS are
outside this feature. The verification model is based on the
[Subresource Integrity specification](https://www.w3.org/TR/sri/).

## Site-scoped script permissions

`--allow` no longer authorizes script loading. It remains the host list for
fetch/navigation allowlist gates. Cross-origin external scripts require an exact
source-origin → destination-origin grant; granting a script does not open fetch,
images/styles, navigation, private networking, or any other capability. Same-origin
scripts still require `--scripts=external`. Script grants work independently of
`--fetch=off|origin|allowlist`.

For Google's CDN, an explicit command-line grant is:

```sh
ntbrowser --scripts=external \
  --allow-script=https://www.google.com=https://www.gstatic.com \
  'https://www.google.com/search?q=hello'
```

Origins include scheme and effective port. Grants accept exact ASCII hostnames
(including punycode) or IP literals, with no paths or wildcards. A grant for one
page origin does not carry over to another site. Repeat `--allow-script` to add
grant pairs. For persistent, explicitly saved grants, put them in a profile file
and launch with `--profile-file FILE`:

```json
{
  "base": "browse",
  "scripts": "external",
  "script-grants": [
    {
      "source": "https://www.google.com",
      "destination": "https://www.gstatic.com"
    }
  ]
}
```

`--script-prompts` (or `"script-prompts": true`) enables reader decisions for
otherwise ungranted cross-origin scripts. The prompt identifies both origins and
the purpose. `Alt+O` admits that pending request once; `Alt+S` grants the origin
pair for this browser session; `Esc` denies. Session grants apply to pending and
future scripts from that source, including after reload, and expire on exit.
The UI does not write profile files; persistent grants are explicit profile edits.

Prompts cannot replace navigation confirmations or interrupt an active editor or
search. Ordinary typing does not approve, mouse actions cannot reach the page
while the prompt is displayed, and approval is disabled when both origins do not
fit on screen. There are at most eight pending prompts, each expiring after 30
seconds, and 128 session grants (128 saved grants per profile). Navigation drops
pending requests; stale or duplicate decisions have no authority. Private-network,
mixed-content, SRI, CORS and response limits remain enforced after approval.
Denied requests report the initiating origin, script URL and reason in the status
line and diagnostic capture.

### DOM event listening

`window`, `document`, DOM nodes and `new EventTarget()` share
`addEventListener`, `removeEventListener` and `dispatchEvent`. The implementation
supports capture/target/bubble phases, function and `handleEvent` object
listeners, capture-sensitive identity, `once`, `passive`, and `{signal}` removal.
Dispatch snapshots the path and each invocation's listeners; removal takes
immediate effect, and a throwing listener reports an error without skipping
other listeners. Window/document/body wheel and touch listeners default to
passive unless explicitly overridden.

`Event` and `CustomEvent` provide cancellation, both propagation-stop methods,
`composedPath`, phase constants, `isTrusted`, and legacy `initEvent` /
`initCustomEvent` through `document.createEvent`. Common `on*` function properties
share listener ordering and limits. Listener exceptions reach window `error`
listeners and `onerror`; cancellation suppresses their default diagnostic.
`UIEvent`, `MouseEvent`, `KeyboardEvent`, `FocusEvent`, `InputEvent`, `MessageEvent`,
`ErrorEvent` and `ProgressEvent` constructors expose their basic initialization
fields. Constructors alone do not produce device, messaging or lifecycle events.

`AbortController` / `AbortSignal` support `abort(reason)`, `aborted`, `reason`,
`throwIfAborted`, `onabort`, and static `abort`, `any`, and `timeout` helpers.
Timeouts consume the existing host timer budget and accept finite nonnegative
millisecond durations representable by Go's timer duration. Signals currently
cancel **event listeners**; they do not add cancellation to `fetch` or image loads.

Event state and callbacks are private to the VM. Synthetic dispatch stays
untrusted and never invokes host navigation, form submission, or reader
activation. It adds no profile authority. Listener and standalone target counts
are bounded by `MaxListeners` (1024 each by default, including the window target);
abort composition has the same source cap, nested dispatch is limited to 64,
and callbacks retain the normal watchdog, heap and clock-resolution limits.
Abandoned targets are weakly held; observed timeout/dependent signals stay
alive until abort or listener removal. Unobserved timeouts are cleared on
collection, and all pending timeouts are cleared on page close. This follows the [DOM event model](https://dom.spec.whatwg.org/#events)
for the supported tree. Shadow DOM retargeting, inline handler attributes,
full specialized/legacy event interfaces, and new browser event producers
(including a full window-load lifecycle) remain outside this implementation.

### Text encoding

`TextEncoder` provides UTF-8 `encode()` and `encodeInto()`, with standard
replacement of lone UTF-16 surrogates and partial writes that never split a
Unicode scalar. `TextDecoder` supports UTF-8, UTF-16LE and UTF-16BE (including
their standard labels), `fatal`, `ignoreBOM`, and streaming `decode()` calls.
It accepts ArrayBuffers and byte ranges from typed arrays/DataViews; detached
buffers and spoofed views are rejected. Legacy character encodings and
TextEncoderStream/TextDecoderStream are not implemented. Unsupported labels
throw RangeError, rather than silently decoding as UTF-8.

These follow the supported portions of the
[Encoding Standard](https://encoding.spec.whatwg.org/#api) and grant no network,
filesystem or storage access. `jsvm.Config.MaxCodecBytes` defaults to 2 MiB:
encoder input is capped in UTF-16 code units, decoder input (including pending
stream bytes) and conversion output in bytes. Oversized calls throw RangeError;
`encodeInto` preflights the write so a limit failure leaves its destination
unchanged. Private decoder state is bounded by the same cap and reclaimed with
the decoder. The existing per-entry watchdog and heap limit still apply.

### Message channels

`new MessageChannel()` creates two page-local `MessagePort` EventTargets.
`postMessage(data)` copies supported data synchronously and delivers a
`MessageEvent` on a later host task. Ports begin paused: `start()` enables
delivery, and assigning `onmessage` starts it automatically. `addEventListener`
alone does not start a port. Delivery preserves FIFO order, yields between
messages, and retains promise checkpoints and the normal watchdog. `close()`
disconnects a port; accepted messages remain deliverable, while future posts
through the disconnected channel are discarded after validation.

Copies preserve cycles and shared references across plain objects, arrays,
ArrayBuffers, typed arrays, DataViews, Dates, Maps and Sets. Primitive strings,
numbers, booleans, null and undefined are supported. Functions, symbols, BigInts,
DOM/host objects, proxies, weak collections, Promises, RegExps, Errors and custom
class instances are not supported. Nonempty transfer lists are also unsupported,
including ArrayBuffer and MessagePort transfers. Unsupported data throws
DataCloneError before enqueue; data is never passed by reference as a fallback.
There is no global structuredClone API, worker transport,
cross-page messaging or BroadcastChannel.

`jsvm.Config` provides `MaxMessagePorts` (128 live endpoints), `MaxMessages`
(256 queued messages), and `MaxMessageBytes` (4 MiB of queued clone charges).
Each copy is additionally capped at 1 MiB, 4096 visited values and depth 64;
charges include value/key overhead, and array length is capped at 4096.
Limit failures throw QuotaExceededError. Started nonempty queues keep their
receivers alive; otherwise entangled wrappers keep each other alive through
private VM state and can be collected together. Page shutdown releases all
ports, payloads and delivery tasks. One private host task drives the queue,
shares the existing timer limit, and cannot be canceled by page timer IDs.
No network, storage or reader-activation permissions are added. The supported
behavior follows [HTML channel messaging](https://html.spec.whatwg.org/multipage/web-messaging.html#message-channels).

### Window messaging

`window.postMessage` (also `self.postMessage` or unqualified `postMessage`)
supports `(data, targetOrigin, transfer)` and `(data, {targetOrigin, transfer})`.
Omitted targetOrigin defaults to `/` (the current document origin); `*` permits
the current receiver regardless of origin. Other targets must be absolute URLs,
parsed with the WHATWG URL implementation and matched by origin, including
scheme and effective port. Invalid targets throw SyntaxError. Nonmatching
origins are silently discarded on their queued delivery task, after cloning.
Target strings are capped at 2048 UTF-16 code units / 8192 UTF-8 bytes.

Only the current window exists as a receiver: no iframe, popup, worker or
cross-page routing is added. Opaque document origins serialize as `null`; `/`
and `*` work locally, while explicit opaque URLs cannot match another opaque
origin. Delivered MessageEvents have `source === window`, the document's
captured origin, cloned data and empty frozen ports. Native delivery is trusted
but grants no reader activation, navigation, network or storage authority.
Both APIs share the channel task queue, clone restrictions and quotas described
above. Nonempty transfer lists remain unsupported. Navigation/page shutdown
cancels queued deliveries instead of forwarding them into the new document.

### Response-size failures

`netpolicy: response too large` means a response crossed its byte cap, not that
JavaScript called a missing API. Page/text navigation is capped at 1 MiB;
generic fetchers default to 2 MiB, and resource policies may set smaller limits.
The cap applies to the decoded HTTP response (after gzip decompression).
Errors identify the resource, limit, observed lower bound and final URL path;
query strings and fragments are omitted from the displayed URL. Diagnostic
capture records navigation body failures as `navigation_error`, and fetcher
failures as `network_error`. A server's declared Content-Length is not treated
as proof of the decoded response size. These diagnostics do not raise the caps;
page/DOM/render and script budgets remain separate constraints.

### Single-window relationships

With frames disabled, `window.top`, `window.parent`, `window.frames`, and `document.defaultView`
refer to the current page's global object. `window.length` is zero;
`window.opener` and `window.frameElement` are null; `window.closed` is false.
The replaceable `parent`, `frames`, `length`, and `opener` properties may be
shadowed by page code; assigning them creates no host relationships. With frames disabled (the default), inserting an iframe creates no child
browsing context. With frames enabled, the isolated relationships below apply.
Popups remain unsupported. Navigation through an
alias still requires the existing script-navigation and navigation gates,
and `postMessage` through an alias uses the same bounded page-local queue.
See the [HTML Window interface](https://html.spec.whatwg.org/multipage/nav-history-apis.html#the-window-object).

The status line labels an individual failed image as `[image warning]` and
other rendering/resource warnings as `[page warning]`. These warnings do not
replace a load failure, script error, policy-blocked navigation, or pending
navigation confirmation. Full resource errors remain in diagnostic captures.

### Address entry

Run `ntbrowser` without a URL to open the address prompt, or press **Ctrl+L**
while browsing. Type or paste an HTTP(S) URL and press **Enter**; a hostname
without a scheme uses HTTPS. **Ctrl+U** clears text before the cursor and
**Esc** cancels. Terminal paste is supported without invoking clipboard tools.
The address prompt captures input locally; typed text is not sent to the page.
Navigation still obeys the profile's navigation, host, and network restrictions.
Local files must be selected on the command line at startup, before lockdown.

### Document readiness

`document.readyState` is host-owned: `loading` while the current serial parser
script sequence runs, then `interactive` before `DOMContentLoaded`, and
`complete` after the initial render discovers resources and supported image,
stylesheet, and dynamic-script loads settle. Failed/denied resources settle too;
timers and fetch requests do not delay completion. Each transition dispatches
`readystatechange` on document. Completion dispatches a non-bubbling window
`load` event with the legacy document target, once per page. Later scripts can
check readiness instead of waiting for an event that has already happened.
This follows ntbrowser's existing serial script scheduling, not full browser
parser/async/defer scheduling. Script-disabled profiles dispatch no lifecycle
callbacks. Node `ownerDocument` points to the current document, whose
`defaultView` points to the single window; document itself has no owner.

These lifecycle bindings do not implement HTML template parsing via
`innerHTML`, iframe browsing contexts, or POST form submission. Loading
reCAPTCHA's scripts successfully therefore does not imply a usable challenge.

### Bounded HTML fragments

Elements support `innerHTML` reads and atomic replacement. Parsing uses the
receiving element's HTML context (including table repair), with a 64 KiB UTF-8
input cap and 4096-token cap before parsing. The resulting nodes also fit the
existing DOM budgets, including retained detached nodes. A failed replacement
leaves the old subtree intact. Setting null clears the children.

Fragment script elements remain inert after insertion, movement, or attribute
changes, and event-handler attributes retain the existing inert policy. The
getter includes inert scripts but excludes renderer-injected IDs and styles.
Images and stylesheets still require subresource permission. This is local DOM
parsing, not permission to execute generated code or load iframe documents.
Fragment parsing is bounded but not preemptible inside x/net/html.


### Isolated scripted iframes

Enable same-origin child pages with `--frames=origin`. To admit additional
destinations, use `--frames=allowlist --allow-frame=https://parent.example=https://child.example`.
These grants authorize frame documents only; external child scripts still
require `--scripts=external` and their own source/destination script grants.
All built-in profiles leave frames off.

Each attached iframe gets an independent DOM, JavaScript runtime, and bounded
raster viewport. `srcdoc` takes precedence over `src`; an empty source creates
an empty document. Children expose `parent` and `top`; parents expose
`contentWindow`, indexed `frames`, and `length`. Messages report the sender's
origin and a stable `event.source` proxy; `targetOrigin` is checked before
delivery. This first cross-realm wire format accepts JSON-compatible data.
Cycles, undefined, non-finite numbers, special collection/buffer types, and
transfers are rejected. Page-local messaging retains its existing clone support.

By default this mode isolates even same-origin frame DOMs:
`contentDocument` is null and proxy document/location/storage access throws
`SecurityError`. The separate `--frame-dom=same-origin` gate enables the bounded
DOM bridge described below; Location and storage access remain denied. Child cookies and session storage are disabled. Sandbox
supports `allow-scripts` and `allow-same-origin`; other tokens fail closed.
Opaque sandbox children report origin `null`, cannot use fetch, and cannot
create descendants. Embedding restrictions are enforced conservatively:
X-Frame-Options and supported frame-ancestors checks apply; CSP sandbox,
combined policies, and restricted nested embeds are rejected.

Limits: four live children, two levels, 64 document loads per root page,
256 KiB fetched HTML (64 KiB srcdoc), 2,000 child DOM nodes, and 1024×768 pixels
per child viewport. The frame tree shares the network in-flight semaphore and
a 64-message/4 MiB queue. Each message is at most 1 MiB. Removal and replacement
cancel children and discard stale messages, paints, and input.

Child pixels are embedded as renderer-only images; class and inline style are
preserved, but iframe tag selectors are not implemented. Pointer clicks and focused keyboard events route to the displayed
child generation. Native form editing, link/form navigation, cross-window JavaScript globals,
and MessagePort transfers remain unsupported. Same-origin child Location
navigation requires both `--script-navigation=origin` and `--nav=free`;
other navigation modes fail closed. Srcdoc currently uses its parent's URL
for resolution and Location reporting rather than exposing about:srcdoc.

This enables isolated scripted embeds; it does not establish reCAPTCHA
compatibility. See the [HTML iframe specification](https://html.spec.whatwg.org/multipage/iframe-embed-object.html#the-iframe-element)
and [cross-document messaging](https://html.spec.whatwg.org/multipage/web-messaging.html#crossDocumentMessages)
for the broader browser interfaces.


### Frame scrolling and same-origin DOM sharing

With frames enabled, wheel gestures over a child scroll its viewport without
scrolling the parent. Clicking or wheeling inside a child focuses its scrolling:
arrows, PageUp/PageDown, Home/End stay in that frame; Escape returns scrolling to
the parent. Nested gestures route to the deepest displayed frame. Gestures at
a frame boundary stay contained instead of chaining into the parent.

Child scripts can use `scroll`, `scrollTo`, `scrollBy`, `scrollX/Y`, and
`pageXOffset/YOffset`. Numeric and options-dictionary calls are supported;
smooth requests currently move immediately. Scroll events are coalesced.
Layout height is capped at 16,384 CSS pixels while each resident child raster
remains at most 1024×768 pixels. Short documents are padded to their viewport
instead of stretched. Input uses the offset attached to the displayed raster,
and stale generations are rejected. The renderer currently bounds horizontal
overflow to the layout width; arbitrary element scroll containers are not added.

Use `--frames=origin --frame-dom=same-origin` to enable shared DOM access.
For cross-origin frame admission, keep using explicit frame origin grants;
those grants **never** authorize cross-origin DOM access. DOM sharing requires
the same non-opaque HTTP(S) origin, including port. It does not implement
`document.domain` relaxation. A sandbox without `allow-same-origin` remains opaque.

After the child's load event, `iframe.contentDocument`,
`iframe.contentWindow.document`, and same-origin `parent.document` expose
live, revision-bound wrappers. Supported operations include document/body/head
and node traversal, selectors, attributes, `textContent`, bounded `innerHTML`,
form `value`, element/text creation, insertion, and removal. Wrappers preserve
identity within the calling realm; detached referenced nodes stay alive and
charged. Each calling realm may retain at most 256 distinct foreign handles
until it closes; child collections are bounded snapshots. Mutation input is
at most 64 KiB and retains the target DOM's existing budgets.

Every access rechecks origin, endpoint revision, attachment, and source/sandbox
attributes. Removing or replacing a frame revokes old handles immediately,
including earlier in the same script turn. Cross-origin `contentDocument`
returns null; denied or stale proxy accesses throw `SecurityError`.
Same-origin `contentWindow.scrollTo/scrollBy` uses the same gate.
Window proxies follow navigation; retained document/node wrappers do not.

Frame-tree turns are serialized to make synchronous DOM operations safe;
Goja values never cross runtime boundaries. The bridge does not run foreign
event callbacks, expose arbitrary window globals, adopt nodes between documents,
or prepare executable classic scripts. Creating/editing/moving executable
script nodes is rejected; innerHTML fragment scripts remain inert. Resource
and iframe effects continue through the target profile's existing gates.
Event-listener registration and activation through foreign wrappers fail with
`NotSupportedError`. This is a bounded DOM bridge, not a complete cross-realm
browser object model; initial about:blank access before child load is not added.

The origin boundary follows the [HTML same-origin definition](https://html.spec.whatwg.org/multipage/browsers.html#same-origin);
scroll method names and arguments follow [CSSOM View](https://drafts.csswg.org/cssom-view/#dom-window-scrollto).

### Inline styles and mutation observers

Elements expose a stable, live `style` object backed by the real style attribute:
named CSS properties (including camelCase aliases), `cssText`, `cssFloat`,
`length`/`item()`, and `getPropertyValue`, `getPropertyPriority`,
`setProperty`, and `removeProperty`. Common properties and bounded custom
properties are supported. Declaration input is capped at 64 KiB, with the
existing DOM attribute and total-byte limits still enforced. This is an inline
declaration subset of [CSSOM](https://drafts.csswg.org/cssom/#the-cssstyledeclaration-interface):
it does not add computed styles, stylesheet rules, full value grammar validation,
or shorthand expansion. The renderer still determines which declarations paint.

`MutationObserver` supports `observe`, `disconnect`, and `takeRecords`;
attribute filters/old values, child lists, character data, and subtree observation
follow the [DOM API](https://dom.spec.whatwg.org/#interface-mutationobserver).
Records are delivered asynchronously through the microtask queue, under the normal
watchdog. Text nodes expose `data` and `nodeValue`. Removed subtrees remain
observable until delivery; fragment replacement emits one child-list record.
Record node lists are read-only snapshots with indexing, `item()`, and iteration.

Each runtime allows 64 active observers, 256 registrations, 1024 pending records,
1 MiB of charged record data, 1024 transient registrations, and 128 callback
deliveries per entry. Queue or delivery overflow disconnects that runtime's
observers, releases retained nodes, logs one bounded diagnostic, and rejects further
observation. Registration and filter limits reject the attempted registration.
These local APIs grant no network, script, or filesystem authority.

Same-origin foreign elements expose `style` through the existing frame DOM gate.
Observers register only on their own runtime's nodes; foreign writes notify the
target runtime's observers at its next owner checkpoint. No foreign callback
runs synchronously in the calling runtime.
