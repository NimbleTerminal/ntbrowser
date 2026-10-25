# ntbrowser: subresource loading (stylesheets and images) — design

Date: 2026-10-25 · Status: draft v2, builder-checked (one question open for Evan: §11), awaiting approval · Author: Claude (Fable 5.1) with Evan
Extends: `2026-10-25-ntbrowser-architecture-design.md` (v4, implemented). Everything there stands; this document adds one capability and the policy around it.

## 1. Intent

Loading `https://news.ycombinator.com/` today gives the right structure and working links, but default styling and `[image: ]` placeholders: v1 renders only inline `<style>` and never fetches a `<link rel="stylesheet">` or an `<img>`. Evan's bar is "the styling has to be right", on arbitrary small pages, so external stylesheets must be fetched and applied faithfully and images must be shown. Library users must be able to lock this down, because fetching subresources is the first thing in ntbrowser that makes requests a page chose to origins the user did not.

## 2. Goals, non-goals, anti-goals

**Goals**

- A page's external stylesheets are applied before its first frame, in cascade order, exactly as if they were inline.
- `<img>` elements show their images, including SVG, sized by nthtml's existing rules; images that arrive later re-flow the page without refetching anything.
- One subresource policy, set by the library user and exposed as a flag: browser-like (any public origin) by default, or confined to the page origin plus the allowlist.
- Every subresource request goes through `netpolicy`'s address-vetting dialer, with no cookies, no `Referer`, bounded bodies and counts.
- The loader talks to the network through a transport interface so a browser (CORS-bound) transport can be added without touching the loader.
- Scripts never observe any of this: the DOM they see is unchanged; stylesheets and images are host work.

**Non-goals (this round)**

- Running ntbrowser in the browser. `js/wasm` stays compile-only; the new package compiles for it.
- Web fonts, CSS `url()` backgrounds, `@import`, `<picture>`/`srcset`, `<video>`, favicons. nthtml renders none of the first two, so fetching them buys nothing yet (HN's vote arrows are a CSS `background: url(triangle.svg)` and will stay invisible until nthtml paints backgrounds).
- A cross-navigation cache. Pages are always re-fetched (v4 decision 16); subresources follow.
- Changing `<script src>` handling: `-external-scripts` stays its own switch.

**Anti-goals** (failure even if it works)

- A subresource request that bypasses the dialer's address vetting, carries credentials, or follows a redirect without re-admission.
- A page that can make the loop or the UI wait on the network: subresource fetches are off the VM goroutine and the first frame is bounded by a timeout.
- Image decoding that can exhaust memory: every decode is size-checked before pixels are allocated, and the per-page pixel total is capped.
- Injected CSS reaching nthtml with a `</style>` inside it, or counting outside the serialized-size budget.

## 3. What recon established

| Finding (nthtml v0.1.0 unless noted) | Consequence |
| --- | --- |
| `document.Options.Images` is keyed by the **raw `src` attribute value** (`layout.go:424`, `l.opts.Images[n.attrs["src"]]`), not a resolved URL. An `<img>` with no entry renders the text `[image: <alt>]`; with an entry it is sized from `width`/`height` attributes and intrinsic size (`imageSize`). | The loader keys its map by the attribute value as the DOM holds it (unescaped) and resolves against the page base only to fetch. Spacer GIFs (`s.gif` width=N height=1) lay out correctly once the 1×1 image exists. |
| `document.DecodeImage` decodes GIF/JPEG/PNG only, caps input at 1 MiB, dimensions at 4096 and area at 4 Mpx, and reads the config before decoding. `golang.org/x/image/webp` is already in the module graph. | The loader uses `DecodeImage` for raster formats and imports the webp decoder (registers with `image`); no other decoders. |
| `document.DefaultDiagramRenderer{}.RenderDiagram(ctx, "svg", source, w, h)` rasterizes an SVG document to an `image.Image` with ntsvg, bounded. | `<img src="x.svg">` (HN's logo) is rendered through it at the element's `width`/`height` attributes (default 64 px, cap 1024). |
| nthtml collects every `<style>` element in document order (`document.go:251`) and `parseCSS(text, Rules-len(rules))` **fails the whole parse with `ErrLimit`** when the rule budget (`DefaultLimits.Rules` 256, `MaxLimits.Rules` 4096) is exceeded. `<link>` elements are ignored. | External CSS is injected as `<style>` text at the `<link>`'s position; the loader counts rules per sheet and drops a sheet that would exceed the budget (warning), so a big stylesheet can never turn into a blank page. `loop.Config.RenderLimit.Rules` default rises to 2048. |
| nthtml's CSS engine has no `@import`, no `url()`, no `background-image`. | Not fetched; listed as non-goals. |
| **nthtml v0.1.0 `parseCSS` (`css.go:492`) has no at-rule parsing**: it cuts on the next `{`/`}`, so for `@media X { A{} B{} } C{}` rule A is lost, B applies **unconditionally**, and C is swallowed; `@import`/`@font-face`/`@charset` each eat the rule after them. Verified by the builder check with a probe; HN's `news.css` has 14 `@media` blocks, and injecting it verbatim renders the mobile layout (header on two lines) at 800 px. The same bug already affects inline `<style>`. | Faithful styling needs at-rules handled somewhere: this is the open question in §11 (fix nthtml, or a host-side pre-pass). |
| nthtml counts **every comma-separated selector** as a rule (`a,b,c{}` is three) and `ErrLimit`s the parse past the budget. | `CountRules` counts selectors per block, not blocks. |
| `netpolicy.Fetcher.Do` fails fast with `ErrBusy` when its semaphore is full; it does not queue. | The `Session` owns its own `MaxInFlight` queue and gives its `Fetcher` a cap at least as large. |
| Past `Limits.Height` (16384 px) nthtml fails the **whole render** with `ErrLimit`; sized images make pages taller. | A frame that fails with `ErrLimit` is retried once with `Images` nil and a warning, so an image-heavy page degrades to placeholders instead of a blank viewer. |
| `document.DecodeDataURL` requires `;base64` and cannot do SVG; ntsvg has no `<image>`/`href` support and touches neither filesystem nor network. | SVG data URLs go to the diagram renderer; SVG images are not an SSRF path. |
| `Fetcher.Resolve` rejects refs containing whitespace; `dom.Limits.TextBytes` (64 KiB) caps attribute values, so a `data:` image is at most 64 KiB. | The `Session` trims `src`/`href` before resolving but keys results by the raw value; the 64 KiB data-URL cap stands. |
| HN serves `news.css` as `text/css` (a `HEAD` probe earlier showed `text/html`; `GET` is `text/css`); 7.4 KB, 109 blocks, more selectors. Real sites serve CSS and images from CDNs on other origins. | The stylesheet gate is strict like a browser's: `text/css`, plus `application/octet-stream` when the path ends in `.css`; an HTML interstitial or soft-404 is never injected as CSS. The default policy must admit other origins or most pages stay unstyled. |
| `netpolicy.acceptableContentType` admits text/JSON/XML/JS only; `Fetcher.Do` returns the body as a string; `MaxInFlight` is per `Fetcher`. | `Request` gains a `Kind` (document, style, image) that selects the content-type set; `Response.Body` stays a string (≤ 1 MiB). Subresources use their own `Fetcher` with their own in-flight cap. |
| v4 `Policy.AnyOrigin` is documented "navigation only, never for a page's `fetch()`". | It becomes "never for a page's `fetch()`"; the subresource fetcher may carry it when the library user chooses the browser-like mode. |
| `dom.Tree` accounts every byte the serializer emits against `Limits.TotalBytes` (1 MiB = nthtml input cap). | Injected CSS is charged to the same budget when set; a sheet that does not fit is dropped with a warning. |

## 4. Architecture

```
                 VM goroutine (loop)                          worker goroutines
 LoadPage ──► dom.Parse ──► subres.Session.Start(tree) ──► fetch <link rel=stylesheet> (ordered)
                                 │                        ──► fetch <img src> (bounded, concurrent)
      runScripts, timers, rAF    │  completions posted to the VM goroutine
                                 ▼
  startRender: tree.Serialize() ── <style> injected at each loaded <link> ──► document.Render(opts{Images: session.Images()})
                 ▲                                                                      (render worker)
   first frame held until all stylesheets settled or StyleTimeout (1.5 s)
   images arriving later: mark dirty → next coalesced frame
```

| Package | New or changed | Responsibility |
| --- | --- | --- |
| `subres` (new) | `Transport`, `Config`, `Mode`, `Session`, rule counting, decoding | Discover, fetch, decode and cache a page's stylesheets and images; never touches nthtml types except `document.DecodeImage`/`DefaultDiagramRenderer` and `image.Image`. |
| `netpolicy` | `Request.Kind`, `KindDocument/KindStyle/KindImage`, content-type sets per kind, `Policy.AnyOrigin` doc | The only socket owner; unchanged dialer, redirects, cookies, headers. |
| `dom` | `Tree.SetStyle(href, css string) error`, `Tree.LinkedStyles() []string`, `Tree.ImageSources() []string`, serializer injection, byte accounting | The tree knows which `<link>`s have CSS and emits it in place. |
| `loop` | `Config.Subresources`, `Page.subres`, first-frame gating, dynamic discovery at `startRender`, `Options.Images` | Orchestration; completions on the VM goroutine. |
| `app`, `cmd` | build the subresource `Fetcher` per page from the mode; `-subresources any|origin|off` | Policy plumbing. |

## 5. Decisions (mine unless marked)

1. **Policy is the library user's, with three modes** (Evan: "give library users the ability to control that, so they can lock it down"). `subres.Mode`: `ModeAny` (default, browser-like: any public http(s) origin), `ModeOrigin` (page origin plus `-allow`, the same rule as `fetch()`), `ModeOff` (nothing fetched; v1 behaviour). The binary exposes `-subresources any|origin|off`. In every mode the dialer vets addresses, redirects are re-admitted, no cookies or `Referer` are sent, bodies are capped.
2. **The transport is an interface**: `type Transport interface { Do(ctx context.Context, r netpolicy.Request) (netpolicy.Response, error) }`. `*netpolicy.Fetcher` satisfies it. A future browser transport implements it over the platform `fetch` and is bound by CORS; the loader does not know the difference, a denied resource just fails like any other and the page degrades to unstyled text or a placeholder (Evan: "we can enable/disable CORS native and are stuck with it in browser").
3. **Stylesheets are render-blocking for the first frame, bounded by `StyleTimeout` (1.5 s).** Browsers block rendering on CSS to avoid a flash of unstyled content; a terminal re-flow is far more visible than a browser repaint, so the first frame waits until every stylesheet has succeeded or failed, or the timeout fires, whichever is first. Scripts, timers and `DOMContentLoaded` are **not** delayed (simpler than the browser's script-blocks-on-CSS rule, and the DOM they see is the same). Later-added stylesheets never block.
4. **Images never block anything.** Each decoded image marks the tree dirty and rides the next coalesced frame, so a page with twenty images re-flows a handful of times, not twenty.
5. **CSS is injected at the `<link>`'s position**, as a `<style>` element the serializer writes immediately after the `<link>`, so the cascade order between external and inline sheets is preserved. The DOM has no such node; scripts cannot see or edit it. The text passes `sanitizeCSS` (`</` → `<\/`) and is charged to `TotalBytes` when set.
6. **Rule budget is enforced by the loader, not discovered by a failed render.** `subres.CountRules(css)` counts **selectors** the way nthtml does (each comma-separated selector of each block; over-counting an invalid group is safe), strings and comments skipped; a sheet that would push the page over `RenderLimit.Rules` (default now 2048, cap `MaxLimits.Rules` 4096) is dropped with a warning in the status line. Inline `<style>` the page already had counts first.
7. **Discovery happens twice**: at `LoadPage` (so the first frame can wait for the sheets) and at every `startRender` (so `img.src = …` and `<link>` elements created by scripts are picked up). A source is requested at most once per page; results are keyed by the raw attribute value.
8. **Images are decoded on the worker that fetched them**, under `DecodeImage`'s caps, then counted against a per-page pixel budget (`MaxImagePixels`, default 16 Mpx ≈ 64 MiB RGBA). Over budget, later images are placeholders with a warning. SVG goes through `DefaultDiagramRenderer` at the element's declared size (default 64 px, cap 1024 per side) with the same context deadline.
9. **Caps**: `MaxStylesheets` 16, `MaxImages` 64, `MaxSheetBytes` 256 KiB, `MaxImageBytes` 1 MiB (nthtml's), `MaxInFlight` 6, per-request `Timeout` 10 s, `StyleTimeout` 1.5 s. Beyond a count cap the extra references are ignored with one warning.
10. **Content-type gates per kind** (`netpolicy.Request.Kind`): `KindDocument` (today's set), `KindStyle` (`text/css`, or `application/octet-stream` when the URL path ends in `.css`; my call after the builder check, matching browsers in standards mode), `KindImage` (`image/png`, `image/jpeg`, `image/gif`, `image/webp`, `image/svg+xml`). Nothing else decodes anything.
11. **No cross-page cache.** Subresources live in the page's `Session` and die with it (`dropPage` cancels its context). A small cross-page LRU is left to the builder as an option if real use shows refetching hurts.
12. **Data URLs** (`src="data:image/png;base64,…"`) are decoded locally with `document.DecodeDataURL`, under the same byte and pixel budgets, never fetched. SVG data URLs go through the diagram renderer.
13. **`<script src>` is untouched**: it keeps `-external-scripts` and the `fetch()` policy, because it is code, not content.
14. **No mixed content** (my call): an `https:` page may not load `http:` stylesheets or images in any mode; the subresource `Fetcher` carries the page scheme and `Allowed` refuses a downgrade (redirects included). A legacy `http:` page may load either. Browsers block the same; a network attacker must not be able to restyle a secure page or watch which pages are read.
15. **Flags stay orthogonal** (my call): `-nav allowlist` does not change the subresource default; the README states the lockdown pair (`-nav allowlist -subresources origin`) in one line.
16. **A failed frame is retried once without images** when nthtml returns `ErrLimit` (page height or pixels), with a warning; images are the only thing the host added, so removing them is the only degradation that cannot break content.
17. **Privacy note, not a mechanism**: `ModeAny` lets a page load a tracking pixel from any public host, exactly as a browser would, minus cookies and `Referer`. `ModeOrigin` is the lockdown. The README says so in one sentence next to the flag.

## 6. Interfaces (signatures the plan will pin)

```go
// package subres
type Mode int
const ( ModeAny Mode = iota; ModeOrigin; ModeOff )
func ParseMode(s string) (Mode, error)

type Transport interface {
	Do(ctx context.Context, r netpolicy.Request) (netpolicy.Response, error)
}

type Config struct {
	Mode            Mode
	MaxStylesheets  int           // 16
	MaxImages       int           // 64
	MaxSheetBytes   int           // 256 KiB
	MaxImagePixels  int           // 16 << 20
	MaxInFlight     int           // 6
	StyleTimeout    time.Duration // 1500 ms
	Timeout         time.Duration // 10 s per request
	RuleBudget      int           // remaining nthtml rules the page may add (loop passes RenderLimit.Rules - inline count)
}

// Session is one page's subresources. All methods except the fetch workers run on the loop's VM goroutine.
type Session struct{ /* unexported */ }
func NewSession(ctx context.Context, cfg Config, t Transport, base string, post func(func())) *Session
func (s *Session) Discover(sheets []string, images []string)   // request anything not yet requested; sheets in order
func (s *Session) StylesPending() bool                           // any first-discovery stylesheet unsettled
func (s *Session) Style(href string) (css string, ok bool)      // loaded, rule-checked, sanitized
func (s *Session) Images() map[string]image.Image               // raw src → decoded; the map handed to document.Options
func (s *Session) Warnings() []string                           // drained by the caller; sanitized by app.clean downstream
func (s *Session) Close()                                        // cancels in-flight work
func CountRules(css string) int

// package netpolicy
type Kind int
const ( KindDocument Kind = iota; KindStyle; KindImage )
type Request struct { Method, URL string; Headers map[string]string; Body string; Kind Kind }
// Policy gains: NoDowngradeFrom string // page scheme; "https" refuses http: targets and redirects (decision 14)

// package dom
func (t *Tree) LinkedStyles() []string            // hrefs of <link rel=stylesheet>, document order, deduplicated
func (t *Tree) ImageSources() []string            // raw src of every <img>, document order, deduplicated
func (t *Tree) SetStyle(href, css string) error   // charges TotalBytes; ErrLimit if it does not fit; replaces
func (t *Tree) ClearStyles()

// package loop
type Config struct { …; Subresources subres.Config; SubresourceTransport func(base string) subres.Transport }
```

The `Session` runs its own bounded worker pool (`MaxInFlight`) and queues the rest; it never relies on the `Fetcher`'s semaphore, which fails fast with `ErrBusy`. The `post` callback given to `NewSession` is the loop's `post`: every completion (a decoded image, a settled sheet) becomes a VM-goroutine task that stores the result and marks the tree dirty, so the `Session`'s maps are only ever touched from one goroutine and `Images()` can be handed to the render worker as an immutable snapshot (copy on each `startRender`).

## 7. Cadence changes in `loop`

1. `LoadPage`: after `dom.Parse`, `page.subres = subres.NewSession(page.ctx, cfg, transport(base), base, l.post)`; `Discover(tree.LinkedStyles(), tree.ImageSources())`; `page.styleDeadline = now + StyleTimeout`; one timer armed for it.
2. `scheduleFrame`: if `page.firstFrame && page.subres.StylesPending() && now < page.styleDeadline` → do nothing (the deadline timer or the last settlement re-schedules).
3. `startRender`: `Discover(tree.LinkedStyles(), tree.ImageSources())` again (dynamic additions); for each loaded sheet `tree.SetStyle(href, css)`; `opts.Images = page.subres.Images()` snapshot; serialize; render as before. Warnings drained into the `FrameMsg` (`Result.Warnings` are nthtml's; the loop appends its own to a new `FrameMsg.Warnings` field so `app` shows both).
4. Completion task (posted by a worker): store result, `tree.MarkDirty()` (new, no-op if already dirty), `afterTask` schedules the frame.
5. `dropPage`: `page.subres.Close()`.

## 8. Threat model additions

| Threat | Control | Where |
| --- | --- | --- |
| Subresource SSRF (metadata, LAN, NAT64) | same dialer, same `isForbiddenIP`, redirects re-admitted; `ModeOrigin` for lockdown | `netpolicy` |
| Tracking/beaconing via `img src` | documented; `ModeOrigin`/`ModeOff`; no cookies, no `Referer`, no credentials | `subres`, README |
| Decompression/pixel bombs | `DecodeImage` reads config first, caps 1 MiB / 4096 / 4 Mpx; per-page `MaxImagePixels`; SVG through ntsvg's bounded renderer at a capped size | `subres` |
| Hostile CSS against nthtml's parser | sheet bytes cap, rule count enforced before render, `sanitizeCSS`, nthtml's own bounded parser; a sheet that fails drops alone | `subres`, `dom` |
| Request floods (`img.src` in a loop) | at most `MaxImages` distinct sources per page, `MaxInFlight` 6, per-request timeout | `subres` |
| Page wedges rendering by never settling a sheet | `StyleTimeout` for the first frame; later sheets never block | `loop` |
| Injected CSS escapes `<style>` or the byte budget | `sanitizeCSS`, `TotalBytes` charge at `SetStyle` | `dom` |
| Stale completions after navigation | per-page context cancelled in `dropPage`; completions check `l.page == page` | `loop` |

## 9. Verification the plan must include

- Styled first frame: a page with `<link rel=stylesheet href=/s.css>` where `s.css` is `h1{color:#ff0000}` renders its first frame with a red `h1` box (`Box.Color`), and the httptest server saw exactly one request for `s.css` after five script-driven re-flows.
- Timeout: a stylesheet handler that sleeps 3 s yields an unstyled first frame within ~1.5 s, then a styled frame when it arrives.
- Images: a PNG, a GIF spacer with `width=40 height=1`, an SVG with `width=18 height=18`, a webp, and a `data:` PNG all produce `Tag:"img"` boxes of the expected size; an `image/x-icon` response and an oversize image produce placeholders and one warning each.
- Policy: `ModeOrigin` refuses a CDN stylesheet and image; `ModeAny` loads them; both refuse `http://10.0.0.1/`; `ModeOff` requests nothing (server hit count 0).
- Budgets: the 65th image is not requested; a sheet with 3000 rules is dropped with a warning and the page still renders; a 300 KiB sheet is refused; total pixels over `MaxImagePixels` stop decoding.
- Dynamic discovery: a script that sets `img.src` after load gets its image on a later frame; a script that appends a `<link>` gets its sheet without blocking.
- Injection correctness: the serializer emits the sheet immediately after its `<link>`, sanitized, and `len(Serialize()) <= Bytes()` still holds (extend the dom fuzz seed with a `<link>`).
- Navigation: `dropPage` cancels in-flight subresource requests (a slow handler observes context cancellation).
- Mixed content: an `https:` page's `http:` stylesheet and image are refused in `ModeAny`; an `http:` page may load both.
- Content type: a stylesheet served as `text/html` is refused (placeholder-free, just unstyled) and a `.css` path served as `application/octet-stream` is accepted.
- Height overflow: a page whose images push it past `Limits.Height` renders a second time without images and carries a warning, never a blank frame.
- `GOOS=js` build of `subres`.
- Interactive: HN front page in a recorded PTY shows the orange bar and the logo; a comments page shows indentation (spacer GIFs) — vote arrows will still be missing (nthtml backgrounds), stated as expected.

## 10. Left to the builder

- Exact rule-counting grammar (strings, comments, nested `@media`/`@supports`); a table test with a dozen sheets including HN's.
- Whether `Warnings` are shown once per sheet/image or aggregated per frame.
- A cross-page LRU if needed (decision 11).
- Taskfile/CI: the `wasm` task adds `./subres`.

## 11. Open question for Evan

**At-rules.** nthtml v0.1.0 cannot parse `@media` (or any at-rule): rules inside a media block apply unconditionally and the rule after the block is lost (§3). With `news.css` injected faithfully, HN renders its *mobile* layout at 800 px. Faithful styling therefore needs at-rules handled, and the same defect already affects inline `<style>` today. Two ways:

1. **Fix nthtml** (recommended): teach `parseCSS` to skip `@import`/`@charset`/`@font-face`/`@keyframes` bodies and to evaluate `@media` (`screen`, `all`, `min-width`/`max-width` against `Options.Width`; `print` false), then pin the new nthtml version here. Roughly fifty lines in `css.go` plus tests; benefits every nthtml user and inline styles too; this design then injects CSS verbatim as written. Cost: an nthtml release before ntbrowser can ship this, and §5 decision 15 of the v4 design ("no nthtml work blocks v1") is reopened for this round.
2. **Host-side pre-pass** in `subres`: flatten at-rules before `SetStyle` (drop non-matching `@media`, keep matching bodies, strip the others), re-run on `Resize` since matching depends on width, and apply the same pass to inline `<style>` text in the serializer so both behave alike. Cost: a CSS pre-parser in ntbrowser that must agree with nthtml's, re-flattening per width change, and package ownership changes (`dom` or the loop must transform inline styles).

Which do you want? The rest of this document is the same under either answer, except that option 2 adds a `subres.Flatten(css string, width int) string` component and a `dom` hook.
