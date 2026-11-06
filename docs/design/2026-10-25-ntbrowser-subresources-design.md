# ntbrowser: subresource loading (stylesheets and images) — design

Date: 2026-10-25 · Status: draft v5 (v4 plus the 2026-10-27 external review: §3 rewritten as facts about nthtml v0.1.1 and `main` at 4123b17, load-path holes closed in decisions 1, 3, 5, 6, 7, 10, 17 and new decisions 20–21), builder-checked; awaiting approval of the whole document · Author: Claude (Fable 5.1) with Evan
Extends: `2026-10-25-ntbrowser-architecture-design.md` (v4, implemented). Everything there stands; this document adds one capability and the policy around it.

## 1. Intent

Loading `https://news.ycombinator.com/` today gives the right structure and working links, but default styling and `[image: ]` placeholders: v1 renders only inline `<style>` and never fetches a `<link rel="stylesheet">` or an `<img>`. Evan's bar is "the styling has to be right", on arbitrary small pages, so external stylesheets must be fetched and applied faithfully and images must be shown. Library users must be able to lock this down, because fetching subresources is the first thing in ntbrowser that makes requests a page chose to origins the user did not.

## 2. Goals, non-goals, anti-goals

**Goals**

- A page's external stylesheets are applied before its first frame, in cascade order, exactly as if they were inline; `@import` and `<link media>` behave as in a browser.
- `<img>` elements show their images, including SVG, sized by nthtml's existing rules; images that arrive later re-flow the page without refetching anything.
- CSS `background-image: url(…)` resources are fetched and handed to nthtml the same way (decision 21), so HN's vote arrows and every sprite-based icon appear.
- One subresource policy, set by the library user and exposed as a flag: browser-like (any public origin) by default, or confined to the page origin plus the allowlist.
- Every subresource request goes through `netpolicy`'s address-vetting dialer, with no cookies, no `Referer`, bounded bodies and counts.
- The loader talks to the network through a transport interface so a browser (CORS-bound) transport can be added without touching the loader.
- Scripts never observe any of this: the DOM they see is unchanged; stylesheets and images are host work.

**Non-goals (this round)**

- Running ntbrowser in the browser. `js/wasm` stays compile-only; the new package compiles for it.
- Web fonts (`@font-face` `url()`), `<picture>`/`srcset`, `<video>`, favicons, `cursor:`/`list-style-image` URLs. nthtml paints none of them.
- Form controls, `<iframe>`, SVG `<use>` sprites: content gaps measured in the ten-page sample, none of them styling.
- A cross-navigation cache. Pages are always re-fetched (v4 decision 16); subresources follow.
- Changing `<script src>` handling: `-external-scripts` stays its own switch.
- Evaluating media queries in ntbrowser. Every condition the loader meets is handed to nthtml as `@media` text (decisions 17 and 20); there is one evaluator and it is nthtml's.

**Anti-goals** (failure even if it works)

- A subresource request that bypasses the dialer's address vetting, carries credentials, or follows a redirect without re-admission.
- A page that can make the loop or the UI wait on the network: subresource fetches are off the VM goroutine and the first frame is bounded by a timeout.
- Image decoding that can exhaust memory: every decode is size-checked before pixels are allocated, and the per-page pixel total is capped.
- Injected CSS reaching nthtml with a `</style>` inside it, or counting outside the serialized-size budget.
- A second frame that loses styling the first frame had (budget accounting that drifts between frames).

## 3. What recon established

Facts are about nthtml **v0.1.1** (released 2026-10-26, on the Go proxy, requires nttypeset v0.1.4) unless marked **`main`**, which is nthtml `main` at 4123b17 (2026-10-26 23:36, unreleased, seven commits past v0.1.1). ntbrowser facts are about this repository at 4a4dd0e.

| Finding | Consequence |
| --- | --- |
| `document.Options.Images` is keyed by the **raw `src` attribute value** (`layout.go` `l.opts.Images[n.attrs["src"]]`), not a resolved URL. An `<img>` with no entry renders `[image: <alt>]`; with an entry it is sized from `width`/`height` attributes and intrinsic size. | The loader keys its map by the attribute value as the DOM holds it (unescaped) and resolves against the page base only to fetch. Spacer GIFs (`s.gif` width=N height=1) lay out correctly once the 1×1 image exists. |
| `<img>` with absent or empty `alt` and no `width`/`height` renders nothing; with both attributes it reserves that box silently. **`main`** (4ba6f18) also keeps the link region of an empty-alt image inside `<a>` and exposes `Link.Anchor`. | `loop.TestLinkActivatedMatchesNthtmlLinks` (fixture `<a href><img alt=""></a>`) fails on v0.1.1 because the anchor has no box, and passes again on `main`. The pin task gives the fixture `width="20" height="20"` so it holds on both. |
| `document.DecodeImage` decodes GIF/JPEG/PNG only, caps input at 1 MiB, dimensions at 4096 and area at 4 Mpx, and reads the config before decoding. `golang.org/x/image/webp` is already in the module graph. | The loader uses `DecodeImage` for raster formats and imports the webp decoder (registers with `image`); no other decoders. |
| `document.DefaultDiagramRenderer{}.RenderDiagram(ctx, "svg", source, w, h)` rasterizes an SVG document to an `image.Image` with ntsvg, bounded. | `<img src="x.svg">` (HN's logo) is rendered through it at the element's `width`/`height` attributes (default 64 px, cap 1024). |
| nthtml collects every `<style>` element in document order and `parseCSS(text, Rules-len(rules))` **fails the whole parse with `ErrLimit`** past the rule budget (`DefaultLimits.Rules` 256, `MaxLimits.Rules` 4096). Every comma-separated selector is one rule (`a,b,c{}` is three). **Selectors inside `@media` count at parse time whatever the condition**, `@media print` included (`features_test.go` `TestAtRulesAndMediaQueries`, `Limits{Rules: 2}` fails on three rules inside `@media print`). Bodies of other at-rule blocks (`@supports`, `@font-face`, `@keyframes`, `@layer`) are skipped with brace matching and **not** counted; statement at-rules (`@import`, `@charset`) are skipped to the `;`. `<link>` elements are ignored. | External CSS is injected as `<style>` text at the `<link>`'s position. `subres.CountRules` reproduces exactly this count (decision 6) so a sheet that fits the counter always fits the render; `RenderLimit.Rules` defaults to 4096. |
| `@media` is evaluated at **render** time against `Options.Width` (TTY: columns × 10 px), nested to depth 8, prelude ≤ 1,024 bytes, re-evaluated on every `Render`. The evaluator (`css.go` `evalMediaQueryList`) handles comma lists, `not`, `only`, `and`, `or`; media types `screen`/`all` true, others false; features `width`/`min-width`/`max-width` in any absolute length with `em`/`rem` at a 16 px root (so `(width: 50em)` is true at 800 px); **`main`** adds `prefers-color-scheme` against `Options.ColorScheme`. Unknown features are false. | ntbrowser never evaluates a media condition. `@import … <media>` and `<link media>` become `@media <cond> { … }` wrappers in the injected text (decisions 17, 20), so nthtml applies them on every frame, `Resize` included, with no host work and no second implementation. |
| v0.1.1 parses `:link`, `:visited`, `:hover`, `:active`, `:focus` (the last four never match, the rest of the list survives). **`main`** adds `:is()`, `:where()`, `:has()`, `:not()`, state pseudo-classes, CSS custom properties, `calc()`/`clamp()`/`min()`/`max()`, `vw`/`vh`, `opacity`, `aspect-ratio`, `object-fit`, `display: grid`. | CSS is injected verbatim. No host-side rewriting of selectors or values. |
| v0.1.1 maps presentational attributes (`bgcolor`, `align`/`valign`, `cellpadding`/`cellspacing`, table and cell `width`/`height`, `border="N"`, `<center>`, `<font>`, body `text`/`link`) as hints below author CSS. | Nothing for the host to do. HN's orange bar and centred 85 % column come from these. |
| **`main`** (4123b17) paints `background-image` `url()` layers and gradients, with `background-size`/`-position`/`-repeat`. Lookup (`background.go` `resolveBackgroundURLImage`) is `Options.BackgroundImages[u]`, then `Options.Images[u]`, then an inline `data:image/` decode, where `u` is the `url()` argument **as written, unquoted** (`badge.png`), not resolved. `Document.ImageSourceList()` lists distinct `<img src>` (kind `img`) and `url()` sources (kind `background`) from element styles, `style` attributes and every parsed rule regardless of media; `Document.Render(ctx, Options)` renders an already-parsed document. | Background images are discoverable only after a parse that includes the injected sheets, so they are discovered on the render worker from the same `Document` that is rendered (decision 21), one parse per frame as today. Relative `url()`s inside a fetched sheet must be made absolute by the loader, since nthtml does not know which sheet a rule came from. |
| `netpolicy.Fetcher.Do` always sends `Accept: text/*, application/json;q=0.9`, admits text/JSON/XML/JS content types only (`acceptableContentType`), and **returns the body for any status code**. `Response.Body` is a string; `MaxInFlight` is per `Fetcher` and `Do` fails fast with `ErrBusy` instead of queuing. | `Request.Kind` selects the `Accept` header and the content-type set, and `KindStyle`/`KindImage` additionally require a 2xx status (decision 10); the zero `Kind` is `KindDocument` so `fetch()` is byte-for-byte unchanged. Subresources use their own `Fetcher`; the `Session` owns the queue. |
| `Fetcher.Allowed` rejects `localhost`, `.local`/`.internal` and literal private/loopback addresses **before** it looks at `AnyOrigin`, and the dialer re-checks every resolved IP; both are skipped only with `Policy.AllowPrivate`. `cmd` sets it from `-allow-private`; every `loop` test that talks to `httptest` (a loopback address) passes `AllowPrivate: true`. | The subresource `Policy` copies `AllowPrivate` from the page policy (decision 1) and the plan's `httptest` success paths set it; `ModeAny` without it still refuses `https://127.0.0.1/style.css`, which is the intended production behaviour. |
| v4 `Policy.AnyOrigin` is documented "navigation only, never for a page's `fetch()`". | It becomes "never for a page's `fetch()`"; the subresource fetcher may carry it when the library user chooses the browser-like mode. |
| `Fetcher.Resolve` resolves a reference against `Policy.Origin` (the page URL) and rejects refs containing whitespace. Neither `dom` nor `loop` reads `<base href>`. `dom.Limits.TextBytes` (64 KiB) caps attribute values, so a `data:` image is at most 64 KiB. | `<base href>` stays ignored for subresources too, consistently with links (decision 7). The `Session` trims `src`/`href` before resolving but keys results by the raw value. |
| `dom.Tree` accounts every byte the serializer emits against `Limits.TotalBytes` (1 MiB = nthtml input cap) through `charge(delta)`. | Injected CSS is charged by delta when set and credited when replaced or cleared (decision 5), so a sheet that fits in frame one still fits in frame two. |
| `loop.LoadPage` runs `dom.Parse`, then `runScripts` (inline scripts synchronously; an external script returns early and resumes the sequence from a posted task), then `scheduleFrame`. `DOMContentLoaded` fires when the script sequence ends. | Stylesheets that scripts add before the first frame exist at first paint; the blocking set is defined by time (before the first frame), not by the first `Discover` call (decision 3). |
| Past `Limits.Height` (16384 px) nthtml fails the **whole render** with `ErrLimit`; sized images make pages taller. | A frame that fails with `ErrLimit` is retried once with both image maps nil and a warning, so an image-heavy page degrades to placeholders instead of a blank viewer (decision 16). |
| `document.DecodeDataURL` requires `;base64` and cannot do SVG; ntsvg has no `<image>`/`href` support and touches neither filesystem nor network. | SVG data URLs go to the diagram renderer; SVG images are not an SSRF path. |
| HN serves `news.css` as `text/css; charset=utf-8`, 7,418 bytes, 124 selectors including the twelve `@media` blocks; `.votearrow` (lines 63–81) is `background: url("triangle.svg"), linear-gradient(…)`, a relative URL resolved against the sheet. Real sites serve CSS and images from CDNs on other origins. | The stylesheet gate is strict like a browser's (`text/css`, plus `application/octet-stream` when the path ends in `.css`); the default policy must admit other origins or most pages stay unstyled; the vote arrows need decision 21 (`url()` absolutized, `triangle.svg` fetched as `KindImage`, rasterized SVG). |
| Ten-page stylesheet measurement (all sheets of each page concatenated; selectors counted as nthtml counts them): Wikipedia 242 KiB / 2,600; CNN lite 197 KiB / 35; Ars Technica 157 KiB / 1,772; MDN 134 KiB / 1,039; go.dev blog 115 KiB / 1,285; jvns.ca 43 KiB / 698; Lobsters 42 KiB / 545 (4 `@import`); Python docs 39 KiB / 548 (1 `@import`); HN 7 KiB / 124; NPR text 2 KiB / 20. | With `RenderLimit.Rules` at nthtml's maximum 4096 and `MaxSheetBytes` 256 KiB, every page in the sample fits whole. Past either cap the sheet that crosses the line is dropped whole with a warning and later sheets are still tried against the remainder (decision 9); that is the stated failure mode, not a surprise. |

## 4. Architecture

```
                 VM goroutine (loop)                                worker goroutines
 LoadPage ──► dom.Parse ──► subres.NewSession ──► Discover(sheets, imgs) ──► fetch <link rel=stylesheet> (+ @import, ordered)
                                 │                                        ──► fetch <img src> (bounded, concurrent)
      runScripts, timers, rAF    │  completions posted to the VM goroutine
                                 ▼
  startRender: Serialize() ── <style> injected after each loaded <link> ──► render worker:
                 ▲                                                            document.Parse → d.ImageSourceList() → d.Render(opts{Images, BackgroundImages})
                 │                                                            posts FrameMsg + the "background" sources ──► Discover(…, backgrounds)
   first frame held until every stylesheet seen so far settled, or StyleTimeout (1.5 s)
   images arriving later: mark dirty → next coalesced frame
```

| Package | New or changed | Responsibility |
| --- | --- | --- |
| `subres` (new) | `Transport`, `Config`, `Mode`, `Session`, rule counting, `@import` splicing, `url()` absolutizing, decoding | Discover, fetch, decode and cache a page's stylesheets and images; never touches nthtml types except `document.DecodeImage`/`DefaultDiagramRenderer` and `image.Image`. |
| `netpolicy` | `Request.Kind`, `KindDocument/KindStyle/KindImage`, `Accept` and content-type sets per kind, 2xx gate for the new kinds, `Policy.NoDowngradeFrom`, `Policy.AnyOrigin` doc | The only socket owner; unchanged dialer, redirects, cookies, header allowlists. |
| `dom` | `Tree.SetStyle(href, css string) error`, `Tree.ClearStyles()`, `Tree.LinkedStyles() []string`, `Tree.ImageSources() []string`, serializer injection with the `<link media>` wrapper, `<noscript>` skipping, byte accounting | The tree knows which `<link>`s have CSS and emits it in place. |
| `loop` | `Config.Subresources`, `Page.subres`, first-frame gating, discovery at `startRender`, `Options.Images`/`BackgroundImages`, render worker parses then renders, `FrameMsg.Warnings` | Orchestration; completions on the VM goroutine. |
| `app`, `cmd` | build the subresource `Fetcher` per page from the mode and the page policy; `-subresources any|origin|off` | Policy plumbing. |

## 5. Decisions (mine unless marked)

1. **Policy is the library user's, with three modes** (Evan: "give library users the ability to control that, so they can lock it down"). `subres.Mode`: `ModeAny` (default, browser-like: any public http(s) origin), `ModeOrigin` (page origin plus `-allow`, the same rule as `fetch()`), `ModeOff` (nothing fetched; v1 behaviour). The binary exposes `-subresources any|origin|off`. The subresource `Policy` is derived from the page's: `Origin` the page URL, `AllowHosts` the same list, **`AllowPrivate`, `Timeout` and `MaxRedirects` copied**, `AnyOrigin` true only in `ModeAny`, `NoDowngradeFrom` the page scheme (decision 14), `MaxBody` the larger of `MaxSheetBytes` and `MaxImageBytes`, `MaxInFlight` at least the `Session`'s. In every mode the dialer vets addresses, redirects are re-admitted, no cookies or `Referer` are sent, bodies are capped.
2. **The transport is an interface**: `type Transport interface { Do(ctx context.Context, r netpolicy.Request) (netpolicy.Response, error) }`. `*netpolicy.Fetcher` satisfies it. A future browser transport implements it over the platform `fetch` and is bound by CORS; the loader does not know the difference, a denied resource just fails like any other and the page degrades to unstyled text or a placeholder (Evan: "we can enable/disable CORS native and are stuck with it in browser").
3. **Stylesheets are render-blocking for the first frame, bounded by `StyleTimeout` (1.5 s).** Browsers block rendering on CSS to avoid a flash of unstyled content; a terminal re-flow is far more visible than a browser repaint. **The blocking set is every stylesheet (and every `@import` it pulls in) discovered before the first frame has been rendered**, whether it came from the parsed HTML, from an inline script during `LoadPage`'s script pass, or from an external script that resumed before the first frame; one deadline, armed at `LoadPage`, bounds them all. The first frame waits until every sheet in the set has succeeded or failed, or the deadline fires. Scripts, timers and `DOMContentLoaded` are **not** delayed. A stylesheet discovered after the first frame never blocks; it rides the next coalesced frame like an image.
4. **Images never block anything.** Each decoded image marks the tree dirty and rides the next coalesced frame, so a page with twenty images re-flows a handful of times, not twenty.
5. **CSS is injected at the `<link>`'s position**, as a `<style>` element the serializer writes immediately after the `<link>`, so the cascade order between external and inline sheets is preserved. The DOM has no such node; scripts cannot see or edit it. The text passes `sanitizeCSS` (`</` → `<\/`). **Byte accounting is by delta**: `SetStyle(href, css)` with the text the tree already holds is a no-op; with new text it charges `len(new) − len(old)` (plus the fixed wrapper) and on `ErrLimit` keeps the old text; `ClearStyles()` credits everything back; a `<link>` no longer in the tree stops being serialized and its bytes are credited at the next `Serialize`. The budget therefore cannot drift between frames (anti-goal 5).
6. **Rule budget is enforced by the loader, not discovered by a failed render.** `subres.CountRules(css)` counts exactly what nthtml counts (§3): every comma-separated selector of every style rule at any depth inside `@media` blocks, matching or not; nothing inside other at-rule blocks; statement at-rules, strings and comments skipped; an invalid selector group is over-counted (safe). `loop.Config.RenderLimit.Rules` defaults to **4096** (`MaxLimits.Rules`, nthtml's ceiling). Inline `<style>` the page already had counts first; each external sheet (after `@import` splicing, decision 17) is tested against the remaining budget in document order and dropped whole with a status-line warning if it does not fit. Reach: every page in the ten-page sample fits (§3); the failure mode past the ceiling is a dropped sheet, never a blank page.
7. **Discovery happens at `LoadPage` and at every `startRender`** (so `img.src = …` and `<link>` elements created by scripts are picked up), plus the background sources each frame reports (decision 21). A source is requested at most once per page; results are keyed by the raw attribute value or `url()` string. **`<base href>` is ignored**, as it already is for links and `fetch()` (`Fetcher.Resolve` resolves against the page URL); fixing it is a v1-wide change, not a subresource one.
8. **Images are decoded on the worker that fetched them**, under `DecodeImage`'s caps, then counted against a per-page pixel budget (`MaxImagePixels`, default 16 Mpx ≈ 64 MiB RGBA). Over budget, later images are placeholders with a warning. SVG goes through `DefaultDiagramRenderer`: for `<img>` at the element's declared size (default 64 px, cap 1024 per side); for a background at the SVG's own `width`/`height` or `viewBox` size (default 64, cap 1024), since nthtml scales backgrounds by `background-size`. Same context deadline.
9. **Caps**: `MaxStylesheets` 16 (imports included), `MaxImages` 64 (backgrounds included), `MaxSheetBytes` 256 KiB, `MaxImageBytes` 1 MiB (nthtml's), `MaxInFlight` 6, per-request `Timeout` 10 s, `StyleTimeout` 1.5 s. Beyond a count cap the extra references are ignored with one warning. Reach (§3): Wikipedia's 242 KiB of CSS is the largest in the sample and is split across sheets, so no single sheet in the sample approaches 256 KiB.
10. **Per-kind request and response gates** (`netpolicy.Request.Kind`, zero value `KindDocument`). `KindDocument`: today's `Accept`, content types and status handling, unchanged, so `fetch()` sees what it saw. `KindStyle`: `Accept: text/css`; response must be 2xx and `text/css`, or `application/octet-stream` when the URL path ends in `.css` (browsers in standards mode). `KindImage`: `Accept: image/png, image/jpeg, image/gif, image/webp, image/svg+xml` (exactly the decodable types, no wildcard, so a CDN never negotiates AVIF); response must be 2xx and one of those types. Nothing else decodes anything. A non-2xx `text/css` body (a soft 404) is dropped with a warning, never injected.
11. **No cross-page cache.** Subresources live in the page's `Session` and die with it (`dropPage` cancels its context). A small cross-page LRU is left to the builder as an option if real use shows refetching hurts.
12. **Data URLs** (`src="data:image/png;base64,…"`) are decoded locally with `document.DecodeDataURL`, under the same byte and pixel budgets, never fetched. SVG data URLs go through the diagram renderer. Background `data:` URLs are left to nthtml, which decodes them itself.
13. **`<script src>` is untouched**: it keeps `-external-scripts` and the `fetch()` policy, because it is code, not content.
14. **No mixed content** (my call): an `https:` page may not load `http:` stylesheets or images in any mode; the subresource `Fetcher` carries the page scheme and `Allowed` refuses a downgrade (redirects included). A legacy `http:` page may load either. Browsers block the same; a network attacker must not be able to restyle a secure page or watch which pages are read.
15. **Flags stay orthogonal** (my call): `-nav allowlist` does not change the subresource default; the README states the lockdown pair (`-nav allowlist -subresources origin`) in one line.
16. **A failed frame is retried once without images** when nthtml returns `ErrLimit` (page height or pixels), with a warning; images are the only thing the host added, so removing both image maps is the only degradation that cannot break content.
17. **`@import` is resolved by the loader and its condition is handed to nthtml.** nthtml skips `@import` statements, so `Session` scans each loaded sheet's leading `@import` statements (before the first rule, per CSS), resolves each URL against **the importing sheet's URL**, fetches it as `KindStyle` through the same gates, and splices the imported text in place of the statement: verbatim when there is no media condition, otherwise wrapped as `@media <cond> { … }` so nthtml evaluates it on every frame (resize needs no host work). `@import … layer(…)` or `supports(…)` fails closed: statement dropped with a warning. Depth is capped at 3; imports count toward `MaxStylesheets` and the byte and rule budgets; a cycle or a failed import leaves the statement out with a warning. Imports block the first frame with their parent sheet, under the same deadline. The wrapper consumes one of nthtml's eight `@media` nesting levels, which no sheet in the sample approaches.
18. **`<noscript>` is not serialized.** nthtml renders `<noscript>` content as text, which is right for a script-less renderer and wrong under ntbrowser, which runs scripts: pages would show "enable JavaScript" banners. The `dom` serializer skips `<noscript>` subtrees; the DOM keeps them so scripts still see them.
19. **Privacy note, not a mechanism**: `ModeAny` lets a page load a tracking pixel from any public host, exactly as a browser would, minus cookies and `Referer`. `ModeOrigin` is the lockdown. The README says so in one sentence next to the flag.
20. **`<link>` is read the way a browser reads it.** `rel` is a case-insensitive space-separated token list; the element is a stylesheet when the tokens include `stylesheet` and not `alternate`, `href` is non-empty, `type` is empty or `text/css`, and `disabled` is absent. A non-empty `media` attribute other than `all` makes the serializer emit the injected text as `<style>@media <media> { … }</style>`, so `media="print"` sheets are parsed (and counted, as nthtml counts them) but never applied on screen, and `media="(max-width: 600px)"` sheets switch on resize without host work. The wrapper is `dom`'s because `dom` holds the `<link>`; `subres` sees only hrefs.
21. **Background images ride the same machinery, on the render worker's parse.** The render worker runs `document.Parse`, then `d.ImageSourceList()`, then `d.Render(ctx, opts)`, and posts the `background` sources with the frame; the loop hands them to `Discover` as images (same caps, same `KindImage` gate, same decode path) and the next frame's `Options.BackgroundImages` carries them, keyed by the `url()` string as written. Because nthtml resolves nothing, **`subres` absolutizes relative `url()` references in a fetched sheet against that sheet's URL before injection** (a lexical pass over `url(` tokens that leaves `data:`, absolute and fragment references alone; the same tokenizer `Imports` uses), so a CDN sheet's `url(../img/arrow.png)` becomes a key the `Session` can fetch and nthtml can look up; `url()`s in inline `<style>` and `style` attributes resolve against the page base like `<img src>`. Background images are never in the blocking set (they are first visible after the first styled frame, one frame later than `<img>`). **Gate**: this decision needs an nthtml release carrying `Options.BackgroundImages` and `Document.ImageSourceList` (on `main` at 4123b17, unreleased). The plan pins the newest release at plan-writing time; if it predates 4123b17, the background task is last and starts with the pin bump, and until then `.votearrow` stays an empty 10 px box.

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
	MaxStylesheets  int           // 16, @import included
	MaxImages       int           // 64, backgrounds included
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
func (s *Session) Discover(sheets, images, backgrounds []string)  // request anything not yet requested; sheets in order
func (s *Session) StylesPending() bool                             // any blocking-set stylesheet (decision 3) unsettled
func (s *Session) Style(href string) (css string, ok bool)        // loaded, imports spliced, url()s absolutized, rule-checked, sanitized
func (s *Session) Images() map[string]image.Image                 // raw <img src> → decoded; document.Options.Images
func (s *Session) BackgroundImages() map[string]image.Image       // url() string → decoded; document.Options.BackgroundImages (decision 21)
func (s *Session) Warnings() []string                             // drained by the caller; sanitized by app.clean downstream
func (s *Session) Close()                                          // cancels in-flight work
func CountRules(css string) int                                   // decision 6: nthtml's count
type Import struct { URL, Media string; Unsupported bool }        // Unsupported: layer()/supports() present (decision 17)
func Imports(css string) (stmts []Import, rest string)            // leading @import statements and the sheet after them
func Absolutize(css, sheetURL string) string                      // decision 21: relative url() → absolute, against the sheet URL

// package netpolicy
type Kind int
const ( KindDocument Kind = iota; KindStyle; KindImage )          // zero value keeps fetch() unchanged
type Request struct { Method, URL string; Headers map[string]string; Body string; Kind Kind }
// Do: Kind selects Accept and the admitted content types; KindStyle/KindImage also require 2xx (decision 10)
// Policy gains: NoDowngradeFrom string // page scheme; "https" refuses http: targets and redirects (decision 14)

// package dom
func (t *Tree) LinkedStyles() []string            // hrefs of stylesheet <link>s per decision 20, document order, deduplicated
func (t *Tree) ImageSources() []string            // raw src of every <img>, document order, deduplicated
func (t *Tree) SetStyle(href, css string) error   // charges the delta against TotalBytes; ErrLimit keeps the old text (decision 5)
func (t *Tree) ClearStyles()                      // credits every injected sheet
func (t *Tree) MarkDirty()
// Serializer: <noscript> subtrees skipped (decision 18); stylesheet <link> gets its <style> twin, wrapped in @media when the link has media (decisions 5, 20)

// package loop
type Config struct { …; Subresources subres.Config; SubresourceTransport func(base string) subres.Transport }
type FrameMsg struct { …; Warnings []string }     // loader warnings; Result.Warnings stay nthtml's
```

The `Session` runs its own bounded worker pool (`MaxInFlight`) and queues the rest; it never relies on the `Fetcher`'s semaphore, which fails fast with `ErrBusy`. The `post` callback given to `NewSession` is the loop's `post`: every completion (a decoded image, a settled sheet) becomes a VM-goroutine task that stores the result and marks the tree dirty, so the `Session`'s maps are only ever touched from one goroutine and `Images()`/`BackgroundImages()` can be handed to the render worker as immutable snapshots (copied on each `startRender`).

## 7. Cadence changes in `loop`

1. `LoadPage`: after `dom.Parse`, `page.subres = subres.NewSession(page.ctx, cfg, transport(base), base, l.post)`; `Discover(tree.LinkedStyles(), tree.ImageSources(), nil)`; `page.styleDeadline = now + StyleTimeout`; one timer armed for it. Scripts then run as today; a `<link>` they add is picked up by step 3 and, while no frame has rendered, joins the blocking set (decision 3).
2. `scheduleFrame`: if `page.firstFrame && page.subres.StylesPending() && now < page.styleDeadline` → do nothing (the deadline timer or the last settlement re-schedules).
3. `startRender`: `Discover(tree.LinkedStyles(), tree.ImageSources(), page.backgrounds)` (dynamic additions; `page.backgrounds` is the list the previous frame reported); for each `href` in `LinkedStyles()` with `Style(href)` loaded, `tree.SetStyle(href, css)` (no-op when unchanged); snapshot `Images()` and `BackgroundImages()` into `opts`; serialize. The render worker runs `document.Parse(ctx, html, opts.Limits)`, `d.ImageSourceList()`, `d.Render(ctx, opts)` and posts all three results. On `ErrLimit` it retries once with both maps nil (decision 16). Warnings from the `Session` are drained into `FrameMsg.Warnings`.
4. Frame completion (posted by the worker): store `page.boxes`, store the `background` sources as `page.backgrounds` and call `Discover(nil, nil, page.backgrounds)` immediately, send the `FrameMsg`, run frame callbacks.
5. Subresource completion (posted by a worker): store result, `tree.MarkDirty()` (no-op if already dirty), `afterTask` schedules the frame.
6. `dropPage`: `page.subres.Close()`.
7. `Resize`: nothing new; the injected `@media` wrappers are re-evaluated by nthtml on the next render.

## 8. Threat model additions

| Threat | Control | Where |
| --- | --- | --- |
| Subresource SSRF (metadata, LAN, NAT64) via `<img>`, `<link>`, `@import` or `url()` | same dialer, same `isForbiddenIP`, redirects re-admitted; `AllowPrivate` only by the operator's flag; `ModeOrigin` for lockdown | `netpolicy` |
| Tracking/beaconing via `img src` or `background: url()` | documented; `ModeOrigin`/`ModeOff`; no cookies, no `Referer`, no credentials | `subres`, README |
| Decompression/pixel bombs | `DecodeImage` reads config first, caps 1 MiB / 4096 / 4 Mpx; per-page `MaxImagePixels`; SVG through ntsvg's bounded renderer at a capped size | `subres` |
| Hostile CSS against nthtml's parser | sheet bytes cap, rule count enforced before render, `sanitizeCSS`, nthtml's own bounded parser; a sheet that fails drops alone | `subres`, `dom` |
| Soft-404 or interstitial injected as CSS | `KindStyle` requires 2xx and `text/css` | `netpolicy` |
| Request floods (`img.src` in a loop, thousands of `url()`s) | at most `MaxImages` distinct sources per page, `MaxInFlight` 6, per-request timeout | `subres` |
| Page wedges rendering by never settling a sheet | `StyleTimeout` for the first frame; later sheets never block | `loop` |
| Injected CSS escapes `<style>` or the byte budget | `sanitizeCSS`, delta charge at `SetStyle` | `dom` |
| Stale completions after navigation | per-page context cancelled in `dropPage`; completions check `l.page == page` | `loop` |

## 9. Verification the plan must include

- Styled first frame: a page with `<link rel=stylesheet href=/s.css>` where `s.css` is `h1{color:#ff0000}` renders its first frame with a red `h1` box (`Box.Color`), and the httptest server saw exactly one request for `s.css` after five script-driven re-flows. (Every httptest-backed test builds its fetcher with `AllowPrivate: true`, as the existing `loop` tests do.)
- Timeout: a stylesheet handler that sleeps 3 s yields an unstyled first frame within ~1.5 s, then a styled frame when it arrives.
- Blocking set: an inline script that appends a `<link>` during `LoadPage` makes the first frame wait for that sheet too; a script that appends one from a `setTimeout` after the first frame does not block and the sheet appears on a later frame.
- `<link>` reading: `media="print"` leaves the screen layout unchanged; `media="(max-width: 600px)"` applies after `Resize(500)` with no second request; `rel="alternate stylesheet"` and `disabled` are not requested; `rel="Stylesheet preload"` is.
- `@import`: a sheet whose first statement is `@import url(b.css) (max-width: 400px);` loads `b.css` once, does not apply it at 800 px, applies it after `Resize(300)` with the server hit count still 1; `@import url(c.css) layer(x);` is dropped with a warning; a four-deep import chain stops at depth 3; a self-import is dropped; an import URL is resolved against the sheet (`/css/a.css` importing `b.css` requests `/css/b.css`).
- Accept and status: the stylesheet handler sees `Accept: text/css`, the image handler sees the five image types and nothing else, the document path still sees today's header; a 404 served as `text/css` is not injected; a 302 to a `text/css` body is.
- Images: a PNG, a GIF spacer with `width=40 height=1`, an SVG with `width=18 height=18`, a webp, and a `data:` PNG all produce `Tag:"img"` boxes of the expected size; an `image/x-icon` response and an oversize image produce placeholders and one warning each.
- Backgrounds (gated on the pin, decision 21): a CDN-hosted sheet `/css/t.css` with `.v{background:url("arrow.png")}` makes the server see `/css/arrow.png` once and the frame's `Result.Image` carries the PNG's colour inside the `.v` box; an SVG background is rasterized; `background: url(…)` to `http://10.0.0.1/` is refused.
- Policy: `ModeOrigin` refuses a CDN stylesheet and image; `ModeAny` loads them; both refuse `http://10.0.0.1/`; `ModeOff` requests nothing (server hit count 0).
- Budgets: the 65th image is not requested; a sheet with 4,097 selectors is dropped with a warning and the page still renders with its other sheets; a Wikipedia-sized sheet (2,600 selectors, 242 KiB) loads; a 300 KiB sheet is refused; total pixels over `MaxImagePixels` stop decoding; `CountRules` agrees with nthtml (`Parse` with `Limits{Rules: CountRules(css)}` succeeds and with one less fails) on a dozen sheets including HN's and one with `@media print` and `@supports` blocks.
- Byte accounting: a sheet sized within 1 KiB of the remaining `TotalBytes` is styled on frame one and still styled on frame five (`SetStyle` is a no-op when unchanged, delta otherwise); replacing it with a smaller text credits the difference (`Bytes()` decreases).
- Dynamic discovery: a script that sets `img.src` after load gets its image on a later frame; a script that appends a `<link>` gets its sheet without blocking.
- Injection correctness: the serializer emits the sheet immediately after its `<link>`, sanitized and media-wrapped when applicable, skips `<noscript>`, and `len(Serialize()) <= Bytes()` still holds (extend the dom fuzz seed with `<link>` and `<noscript>`).
- Navigation: `dropPage` cancels in-flight subresource requests (a slow handler observes context cancellation).
- Mixed content: an `https:` page's `http:` stylesheet and image are refused in `ModeAny`; an `http:` page may load both.
- Content type: a stylesheet served as `text/html` is refused (placeholder-free, just unstyled) and a `.css` path served as `application/octet-stream` is accepted.
- Height overflow: a page whose images push it past `Limits.Height` renders a second time without images and carries a warning, never a blank frame.
- `fetch()` regression: the existing `jsvm`/`loop` fetch tests pass unchanged (zero `Kind`).
- `GOOS=js` build of `subres`.
- Interactive: HN front page in a recorded PTY shows the orange bar, the logo and (once decision 21 is in) the vote arrows; a comments page shows indentation (spacer GIFs).

## 10. Left to the builder

- The `url(` tokenizer shared by `Imports`, `Absolutize` and `CountRules` (strings, comments, escapes); a table test with a dozen sheets including HN's, Wikipedia's and Lobsters' (`@import`).
- Whether `Warnings` are shown once per sheet/image or aggregated per frame.
- A cross-page LRU if needed (decision 11).
- Taskfile/CI: the `wasm` task adds `./subres`.
- Pin task details: go.mod, the v4 design's Appendix B and the AGENTS.md pin line move to the newest nthtml release; `TestLinkActivatedMatchesNthtmlLinks` gets `width="20" height="20"` on its image.

## 11. Open question for Evan (resolved)

Re-verified 2026-10-26 against nthtml `main` (db867c8, unreleased, which also moves the nttypeset dependency to an unreleased pseudo-version): the post-v0.1.0 CSS work (logical margins, font-size keywords, `visibility`, `text-align: start|end`, `Result.Background`, the source-ID navigation API, WPT 187/200) changes none of the facts below. Four nthtml gaps stand between "stylesheet injected" and "HN looks like HN"; the first three are visible on the front page at 800 px:

1. **At-rules.** `parseCSS` must skip `@import`/`@charset`/`@font-face`/`@keyframes` bodies and evaluate `@media` (`screen`, `all`, `only screen` true; `print` and unknown features such as `-webkit-min-device-pixel-ratio` false; `min-width`/`max-width` against `Options.Width`). Roughly fifty lines in `css.go` plus tests.
2. **Dynamic pseudo-classes.** `parsePseudoClass` should accept `visited`, `hover`, `active`, `focus` like `link` so the existing never-match case is reached and the rest of the selector list survives. A few lines.
3. **Presentational attributes.** Map `bgcolor` → `background-color`, `align`/`valign` → `text-align`/`vertical-align`, `cellpadding` → cell padding, table `width`/`border`, and `<center>` → centred content and auto-margin tables, as the HTML rendering section specifies. A small table in `styled()`.
4. **Empty `alt`** (nice-to-have): render nothing, or an empty box of the attribute size, instead of `[image: ]`.

Two ways:

1. **Fix nthtml** (recommended, and more strongly than in v2): all four are small, they benefit every nthtml user and every inline stylesheet, and items 2 and 3 have no clean host-side form. Then release nttypeset and nthtml, pin the new nthtml here, and this design injects CSS verbatim. Cost: a release train before ntbrowser can ship this, and §5 decision 15 of the v4 design ("no nthtml work blocks v1") is reopened for this round.
2. **Host-side pre-pass** in `subres`: flatten at-rules before `SetStyle` (drop non-matching `@media`, keep matching bodies, strip the others), re-run on `Resize` since matching depends on width, apply the same pass to inline `<style>` text in the serializer, and have the `dom` serializer emit presentational attributes as inline `style`. Cost: a CSS pre-parser in ntbrowser that must agree with nthtml's, re-flattening per width change, package ownership changes (`dom` or the loop must transform inline styles), and item 2 stays broken (subtext links black, comment links not underlined) until nthtml changes anyway.

**Evan's answer (2026-10-26): option 1, fix nthtml.** **Shipped as nthtml v0.1.1 (2026-10-26, on the Go proxy; requires nttypeset v0.1.4).** All four fixes re-verified with the same probes: `@media (max-width:750px)` is skipped at 800 px and `@import` no longer eats the next rule; `.a, .b:visited{}` styles `.a`; `bgcolor`, `<center>` + `width="85%"`, `align`, `valign` and `cellpadding` apply; `alt=""` images reserve their box silently. HN with `news.css` inlined at 800 px renders the orange bar, the centred `#f6f6ef` column, black 10 pt titles and grey subtext links. nthtml `main` at 4123b17 (2026-10-26, unreleased) goes on to cover every item of the nthtml request list, including `Options.BackgroundImages` and `Document.ImageSourceList`, which is what turned background images from a deferred item into decision 21. This round's plan therefore starts by pinning the newest nthtml release and `subres` injects CSS verbatim. No `Flatten` component, no `dom` transform hook, no media-query evaluator in ntbrowser.


### Cookie extension (2026-11-06)

The profiles design's `cookies=session` extension supersedes the unconditional
no-cookie statements above only when explicitly enabled. Subresource policies
share the host's bounded session store; only same-origin requests may send or
receive cookies. Cross-origin resources remain cookieless even when allowed to
load. The default remains off, and no Referer is added.
