# ntbrowser

A secure, non-JIT terminal web runtime: a hardened pure-Go JavaScript interpreter (Goja) drives a mutable DOM that ntbrowser owns, and [nthtml](https://github.com/NimbleTerminal/nthtml) renders it in the terminal.

`ntbrowser` is a terminal reader mode and small-application runtime. It takes an HTML page that may carry JavaScript, runs that JavaScript inside a pure-Go bytecode interpreter with no path to the operating system, lets the script change the page through a narrow proxy API, and shows the result through `nthtml`'s Bubble Tea viewer. The point is a "cognitive shield": the user gets the page's content and legitimate interactivity, and nothing the page does can reach files, sockets, the environment, or the terminal's escape-sequence surface.

## Usage

```
ntbrowser [-allow host] [-allow-private] [-nav free|allowlist|confirm] [-subresources any|origin|off] [-external-scripts] [-width px] [-slice 50ms] [-seed n] URL
```

`URL` is `http(s)://…` or, for the start page only, `file://…`.

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-nav free\|allowlist\|confirm` | `free` | Link navigation: any http(s) origin; confined to the origin plus `-allow`; or ask before leaving the origin |
| `-allow host[,host]` | | Extra hosts `fetch()` (and allowlist navigation) may reach; a leading dot allows subdomains |
| `-allow-private` | off | Permit loopback and private addresses (development only) |
| `-subresources any\|origin\|off` | `any` | Stylesheets, images and CSS backgrounds: any public origin (browser-like); the page origin plus `-allow`; or nothing (v1 behaviour) |
| `-external-scripts` | off | Fetch `<script src>` through the same policy |
| `-width px` | 800 | Initial document width; the terminal's pixel geometry takes over once known |
| `-slice d` | 50ms | JavaScript time budget per entry; three overruns disable the page |
| `-seed n` | 0 | PRNG seed for `Math.random` (0 = time-based) |

Lockdown pair: `-nav allowlist -subresources origin` confines both navigation and subresources to the origin plus `-allow`. In `any` mode a page can load a tracking pixel from any public host, as a browser would, minus cookies and `Referer`; `origin` is the lockdown.

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

MIT, Copyright (c) 2024-2026 Neomantra Corp.
