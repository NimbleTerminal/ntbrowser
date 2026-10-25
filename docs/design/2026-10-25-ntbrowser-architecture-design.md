# ntbrowser: sandboxed JS runtime over nthtml — architecture design

Date: 2026-10-25 · Status: draft v4, two review rounds addressed (builder check; Evan's external review of v3), open questions answered by Evan, re-based on nthtml v0.1.0, awaiting approval · Author: Claude (Fable 5.1) with Evan
Companion spike (compiles, tests pass): `/private/tmp/claude-501/-Users-evan-projects-ntbrowser/e06c4cde-3b2d-437e-9072-cb4b9460b88f/scratchpad/ntbrowser-spike`

## 1. Intent

`ntbrowser` is a terminal reader mode and small-application runtime. It takes an HTML page that may carry JavaScript, runs that JavaScript inside a pure-Go bytecode interpreter with no path to the operating system, lets the script change the page through a narrow proxy API, and shows the result through `nthtml`'s Bubble Tea viewer. The point is a "cognitive shield": the user gets the page's content and legitimate interactivity, and nothing the page does can reach files, sockets, the environment, or the terminal's escape-sequence surface.

## 2. Goals, non-goals, anti-goals

**Goals**

- Run page scripts with zero access to `os`, `net`, environment, or raw terminal output.
- Scripts mutate the page only through host proxies over ntbrowser's own DOM; no Go pointer is ever visible to JS.
- One orchestrator ties terminal input, the JS event loop (timers, promises, fetch completions) and nthtml re-flow into a bounded frame cadence.
- Degrade, never block: a runaway script loses its scripting privilege; the terminal stays responsive.
- Same portability envelope as nthtml: `CGO_ENABLED=0`, builds for `js/wasm`.

**Non-goals (v1)**

- Full web compatibility. The target is *arbitrary small pages* (Evan's answer to Q3), so DOM node lifetime, focus and element clicks follow browser semantics, but only the DOM/Fetch subset listed in §7b exists. No `innerHTML`, forms, CSS object model, `XMLHttpRequest`, `WebSocket`, `localStorage`, workers, or modules in v1.
- Running on `js/wasm` (compile-only; decision 14).
- Cookies or credentials of any kind. Requests are anonymous.

**Anti-goals (failure even if it "works")**

- Any code path where a page-controlled string reaches `os.Open`, `exec`, a dialer, or stdout unescaped.
- A frame loop that can be starved by script or that re-flows on every DOM write.
- Exposing a Go struct to Goja via reflection "because it was convenient".

## 3. What recon established (facts the design rests on)

| Finding | Consequence |
| --- | --- |
| `nthtml/document.Document` is **immutable**: `Parse` → `*Document` → `Render`/`TTY`. There is no mutation API, and `<script>` is treated as `display:none`, never exposed. | ntbrowser owns the mutable tree (`dom` package). Each frame = serialize → `document.Render`. JS never touches nthtml at all, which is stronger than the "proxy around nthtml mutation API" the brief assumed. |
| nthtml **v0.1.0** (tagged 2026-10-25) ships `Model.SetResult`, `Model.Result`, `Model.LinkAtCell`, `Model.Bounds`, `Model.Picture` (→ `ntpicture` `RasterGeometry`/`Crop`), `NewWithOptions{ReflowTypeset}`, `Options.NoImage` + `Result.Paint/Bounds`, `document.MinimalFonts`, `Options.Colors/FontFamilies`, `Limits.LayoutHeight`/`MaxLimits`. `SetResult` **resets scroll, zoom, search and focused link**; the cell→pixel path (`cellArea`, `rasterCellSource`) is still unexported, but every input to it is now public. | Re-flow is `SetResult` in place; the host restores the scroll offset from `Picture().Crop()` with `Pan` (verified: `app.TestReflowKeepsScrollAndNewPageResetsIt`). Element clicks use the host's own copy of the cell mapping, checked cell-by-cell against `LinkAtCell` (`app.TestCellToDocumentAgreesWithLinkAtCell`). Nothing in nthtml blocks v1 any more; §11 lists what would still be nicer upstream. |
| `nthtml.LinkMsg{Link}` is how the viewer hands link activation to the host (keyboard Tab/Enter, and its own press/release-on-the-same-cell mouse rule). In `Result`, `document.Link{ID, Href, Text, Regions}` has `Href = SafeURL(attr)` (parsed and re-serialized, so scheme case and escaping are normalized; hrefs with controls or spaces produce no link at all) and `Text` = the cleaned, whitespace-collapsed label with `alt` text included, or the href itself when the label is empty (`<a href><img></a>`). A `Tag:"anchor"` 1 px marker box exists only for elements with an `id` or `<a name>`; a plain `<a href>` is joined to its link only through `LinkID` on its text runs. Nothing in `Result` names the `<a>` element. | Links stay nthtml's: `LinkMsg` → `loop.LinkActivated` → `anchorFor`, which compares `document.SafeURL(attr)` to `Link.Href` and uses the label only to disambiguate duplicates (`loop.TestLinkActivatedMatchesNthtmlLinks`: an image link with an upper-case scheme and two same-href links all reach their `preventDefault`). The host runs `ClickAt` only for releases where `LinkAtCell` sees no link, so a link click is never delivered twice. `Link.Anchor` (§11) would make the join exact. |
| Goja is pure Go, interpreter-only, builds for wasm; default runtime has only ECMAScript built-ins (no `require`, `process`, `fetch`). **But** its parser honours a trailing `//# sourceMappingURL=` and, without options, calls `os.ReadFile` on a page-controlled path (`parser/statement.go`, `parseSourceMap`; `file.ResolveSourcemapURL`). A bare path is joined relative to the script *name* (ntbrowser names scripts by their https URL, which makes Goja refuse the scheme), but a `file:///…` URL is used verbatim, so `//# sourceMappingURL=file:///etc/hosts` reads that file. Read errors are swallowed and the data only remaps stack-trace positions, so this is an unauthorized file read with a weak oracle (positions, `sources` names), not content exfiltration. **And** the package-level `goja.Compile` passes *no* parser options (`runtime.go: compile(name, src, strict, true, nil)`), so `SetParserOptions` on the runtime does not protect scripts compiled that way. | Page source is compiled with `goja.Parse(name, src, parser.WithDisableSourceMaps)` then `goja.CompileAST`, never `goja.Compile`/`RunString`. Two tests pin it: a static scan forbidding the other entry points, and a behavioral test with a positive control (the unhardened `goja.Compile` on a script pointing at a temp `file://` map visibly remaps a thrown error's `Position().Filename` to `SECRET_SOURCE.js`; the sandbox path on the same script does not). `TestNoSystemImports` additionally pins that `jsvm`/`dom` import none of `os`, `net`, `net/http`, `os/exec`, `syscall`, `io/fs`, `unsafe` (goja itself imports `os`, so that test alone is not sufficient). |
| Goja drains the promise job queue when the outermost `Run*`/`Callable` returns; `Interrupt` may be called from another goroutine; `NewPromise`'s resolve/reject must run on the VM goroutine. | Watchdog = `time.AfterFunc` → `vm.Interrupt`. All completions (fetch, timers) are posted to the VM goroutine's inbox before settling promises. |
| Goja has no per-runtime heap limit, and `debug.SetMemoryLimit` is a soft GC target, not a cap: `a=[];for(;;)a.push(1)` allocates hundreds of MB per 50 ms slice without tripping the time watchdog. | Budgets at the data model (`dom.Limits` ≤ nthtml's, listener/timer caps, 4 KiB log lines, 2 MiB bodies, 1 MiB scripts) **plus** a heap dimension on the watchdog: after every entry, `runtime/metrics` heap-objects bytes above `HeapBudget` (256 MiB) disables the page. One pathological slice can still overshoot; this degrades rather than guarantees. |
| nthtml siblings pin `github.com/neomantra/bubbletea/v2` (wasm-capable fork), Go 1.26.x, Taskfile with `GOWORK=off CGO_ENABLED=0`, MIT, `nimbleterminal.dev/<name>` vanity imports. nthtml is tagged `v0.1.0` (proxy time 2026-10-25T20:36Z); the design was first written against pseudo-version `1bff23c` (Phase 4) and re-based. | ntbrowser is `nimbleterminal.dev/ntbrowser` with the same task layout, `require nimbleterminal.dev/nthtml v0.1.0`, and the same Bubble Tea `replace` line as nthtml (Appendix B is the exact go.mod). |
| nthtml `Model.SetResult` with an `Options.NoImage` result calls `Result.Paint` synchronously; `ModelOptions.ReflowTypeset` makes the viewer call `document.Render` itself inside `SetSize`, on the UI goroutine, at `RasterGeometry().Pixels.X` (or `cols*10`) clamped to [32, 4096]. | The loop keeps painting the full image in its render worker (`NoImage` stays false) and the host leaves `ReflowTypeset` off; instead `app` feeds `loop.Resize(documentWidth(viewer))` on every resize, using the same width rule, so re-flow happens off the UI goroutine under the loop's 2 s budget. |
| Bubble Tea's `Program.Send` blocks on an unbuffered channel. | The loop never calls the sink from the VM goroutine: messages go through a buffered outbox and a forwarder goroutine; log lines are droppable, frames/navigation are not. The UI side only ever `tryPost`s; `LoadPage` falls back to a goroutine on a full inbox and is ordered by generation (decision 23). |
| nthtml `Link.Href` is the raw attribute; relative references need the host to resolve them (`document.ResolveURL(base, href)`). | `loop.LinkActivated` resolves against `Page.Base` before dispatching `click` and before `NavigateMsg`. |

## 4. Architecture

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

Packages (module `nimbleterminal.dev/ntbrowser`):

| Package | Owns | May import |
| --- | --- | --- |
| `dom` | mutable tree, limits, serializer, script extraction, simple selectors | `x/net/html` only |
| `jsvm` | Goja runtime, hardening, time+heap watchdog, `document`/`console`/`fetch`/timers bindings | `goja`, `dom`, `runtime/metrics` — **never** `os`/`net` |
| `netpolicy` | the only socket code: allowlist, SSRF filter, redirect/size/type caps | `net/http` |
| `loop` | VM goroutine, inbox, timer heap, frame scheduler, render worker, `jsvm.Host` impl | `jsvm`, `dom`, `netpolicy`, `nthtml/document` |
| `app` | Bubble Tea model, `nthtml.Model` lifecycle, navigation | `nthtml`, `loop`, `netpolicy` |
| `cmd/ntbrowser` | flags, `os.Open` for `file://` (the only one), `debug.SetMemoryLimit` | everything |

## 5. Decisions (mine unless marked "from brief")

1. **Engine: Goja** (`github.com/dop251/goja`, pseudo-version 2026-10-24). QuickJS bindings need CGO or a wasm host and would break nthtml's `js/wasm` story; `grafana/sobek` is a drop-in fork if ES2023 coverage ever matters. From brief: "Goja or QuickJS in interpreter mode".
2. **ntbrowser owns the DOM; nthtml stays immutable.** Rather than add a mutation API to nthtml (invasive, and it would hand JS a path into the layout engine), every frame re-parses serialized HTML. nthtml's `Parse`+`Render` of an 800px document is sub-frame; the loop coalesces at 33 ms anyway. If profiling says otherwise, `document.Parse` can be skipped by adding a `document.FromTree` later.
3. **One Goja runtime per page, discarded on navigation.** That is the cross-page isolation boundary; no heap survives.
4. **Three layers of budget**: interpreter (call stack, time slice, strikes), data model (`dom.Limits`, listener/timer caps), process (`SetMemoryLimit`). No single layer is trusted.
5. **Event hit-testing is id-based.** Elements that register listeners are serialized with an `id` (minted `nt-h-<NodeID>` when missing) so nthtml's `Result.Anchors`/`Box.Anchor` can be mapped back. Links are matched by `href`+text today (§12 makes it exact).
6. **No `innerHTML`.** Markup injection inside a time slice is both a budget and a parser-differential risk; pages build nodes.
7. **Page text is sanitized twice, and the terminal boundary is the one that counts.** `jsvm.safeString` strips C0/C1 from `console.*` and uncaught-error text and caps lines at 4 KiB, so logs are readable in tests without a host. But page text also reaches the status line as `<title>`, hrefs in the confirm prompt, render warnings, and Go error strings that quote page input (`url.Parse` echoes the URL). `app.clean` therefore drops C0, DEL, C1 and U+2028/U+2029 from everything stored in `title`/`status` and once more in `View`. `ansi.Truncate` preserves escape sequences and is never treated as a sanitizer. `app.TestViewNeverCarriesTerminalEscapes` feeds an OSC 52 title, an SGR log line, a quoted-URL error and a C1 href through `Update` and checks `View()`.
8. **Terminal keys are never swallowed by the page.** `q`/`ctrl+c` and nthtml's own navigation are handled by `app` first; the page gets a `keydown` copy.
9. **Inline scripts only by default.** `-external-scripts` fetches `<script src>` through the same allowlist.
10. **Coarse clock, seeded PRNG.** `Date.now()` has 10 ms granularity; `Math.random` is xorshift, host-replaceable for deterministic replays (matches `ntactor`'s determinism ethos).
11. **Navigation is browser-like by default and configurable** (Evan: "free but user/dev-configurable"). `-nav free` (default): a user-activated link may go to any http(s) origin; there is no `location.assign`, so only the user navigates. `-nav allowlist`: navigation is confined to the current origin plus `-allow`. `-nav confirm`: leaving the origin asks in the status line (Enter/Esc). In every mode the new page's `fetch()` is confined to *its* origin plus `-allow`; `file:` is accepted only for the start page; same-origin includes the port.
12. **Removed nodes live while JavaScript references them** (browser semantics, for arbitrary pages). `removeChild`/`remove`/`textContent=` *detach*; the subtree is *released* from the node budget only when no wrapper in it is alive. The GC story: every wrapper is held through a Go `weak.Pointer` with a `runtime.AddCleanup` that reports the NodeID; listeners live *on the wrapper* (hidden symbol), so a dropped wrapper takes its callbacks with it; wrappers of *attached* listener-bearing nodes are pinned strongly so their listeners keep firing after the page forgets them; detaching demotes to weak, re-attaching promotes. Goja's own WeakMap uses the same `weak`+`AddCleanup` mechanism, so JS reachability equals Go reachability. Under budget pressure the bridge forces a GC, waits ≤10 ms for cleanup reports, and retries once. Only integer counts live in Go (`listenCount`), so budgets survive collection. Known gap: a detached node's listener-bearing *descendant* that JS never referenced directly can be collected while detached, losing its listeners; browsers would keep them.
13. **Minimal focus model and element-level clicks.** `element.focus()`/`blur()`, `document.activeElement`; `keydown` targets the focused element and bubbles to `document`; `event.key` uses DOM names for common keys (`Enter`, `Escape`, `ArrowUp`, …), Bubble Tea names otherwise (`ctrl+s`). Clicks on arbitrary elements: the host converts the terminal cell to a document-pixel area (`app.cellToDocument`, its own copy of nthtml's unexported `rasterCellSource`, tested against `Model.LinkAtCell`) and `loop.ClickAt` picks the smallest painted box with an anchor name; listener-bearing elements always have one (real or minted id). Focus follows clicks to focusable elements. Inline elements paint only a 1 px marker in nthtml, so a listener on a `<span>` is reached through its block ancestor. Keys typed into the viewer's `/` search never reach the page. Element clicks are delivered in v1 through the host copy of the cell mapping (`app/cell.go`, inlined in §8); §11.3 would only make `anchorFor` exact.
14. **`js/wasm` is compile-only in v1.** Under `GOOS=js` Go has no async preemption, so `time.AfterFunc → Interrupt` cannot fire while Goja spins, and `http.Transport.DialContext` (the SSRF vetting) is bypassed by the browser `fetch` transport. CI builds `dom`, `jsvm`, `loop` for wasm; running there needs a cooperative slicing strategy and a build-tagged `netpolicy` (future work, not v1).
15. **No nthtml work blocks v1** (revised after nthtml v0.1.0; Evan had said "yes" to an nthtml-first workstream when `SetResult` did not exist). v0.1.0 shipped `SetResult` and enough public geometry to do the rest host-side, so the first workstream is ntbrowser itself, pinned to `nimbleterminal.dev/nthtml v0.1.0`. The three remaining asks in §11 are filed upstream as nice-to-haves; each has a working host-side substitute in the code, and the substitute is deleted when the upstream change lands.
16. **History: Back, Forward, Reload in v1** (Evan: yes). `⌫`/`⌥←` back, `⌥→` forward, `^R` reload; pages are always re-fetched (no cache), so Back never shows state a script produced earlier.
17. **Re-flow keeps the reader's place.** Because `SetResult` resets the view, `app` reads `viewer.Picture().Crop()` before `SetResult` and `Pan`s back to the same offset after; `Pan` clamps to the new page, so a page that got shorter ends at its bottom. The first frame after a `pageLoadedMsg` skips the restore (`resetView`). Zoom is not restorable through nthtml's public API and resets on every re-flow (§11.1). Search state resets too; a page re-flowing while the reader is mid-search loses the match list, which is accepted for v1.
18. **Element clicks are decided on mouse release, on the same cell as the press**, the rule nthtml itself uses before emitting `LinkMsg`. If `LinkAtCell` reports a link, the host does nothing and lets nthtml's `LinkMsg` carry it (so keyboard and mouse link activation share one path and nothing fires twice); otherwise `cellToDocument` + `loop.ClickAt`. In TTY layout mode element clicks are not delivered (cells index TTY lines, not document pixels); links still work.
19. **Document width follows the terminal's real pixel geometry.** On every `WindowSizeMsg` (and once when the viewer is created) `app` calls `loop.Resize(documentWidth(viewer))` with nthtml's own rule (`RasterGeometry().Pixels.X`, or 10 px per column in glyph fallback, clamped to [32, 4096]). This closes the "pixel width from cell metrics" item that v2 left to the builder.
20. **Top-level navigation and `fetch()` are two policies, and history moves only on success.** `netpolicy.Policy.AnyOrigin` is the navigation policy: any http(s) host, redirects followed across scheme and host (`http://host` → `https://www.host` works), while credentials, local names, forbidden literal and resolved addresses, cookies and the 1 MiB body cap stay enforced by the same dialer. `-nav free` and a confirmed prompt use it; `-nav allowlist`, and a same-origin link under `-nav confirm`, use the strict same-origin+`-allow` policy so a redirect off-origin is refused rather than silently followed. A page's `fetch()` never gets `AnyOrigin`. `app.navigate` carries a navigation sequence and the target history index; `pageLoadedMsg` from a superseded navigation is dropped, and `hIndex`/truncation happen only in the success branch, so Back/Forward always name the page on screen (`app.TestHistoryMovesOnlyOnSuccessAndLatestNavigationWins`, `netpolicy.TestAnyOriginFollowsRedirectsButStillVetsAddresses`).
21. **Every parentless node is reapable, and tree writes are transactional.** `dom.Tree.detached` holds removed subtree roots *and* nodes created but never inserted, so a `createElement` loop that drops its results is reclaimed by the same GC path as `remove()`. `insertBefore(node, node)` is the DOM no-op move. `SetText` allocates the replacement before detaching the old children (and `""` allocates nothing), and the bridge drops strong pins only after the write succeeded; a failed `textContent` write leaves children attached and their listeners firing (`dom` tests, `jsvm.TestCreatedButNeverInsertedNodesAreReleased`, `jsvm.TestFailedTextContentWriteKeepsChildrenAndListeners`).
22. **Memory ceilings are real ceilings.** `dom.Limits.TotalBytes` (default 1 MiB = nthtml `DefaultLimits.Bytes`) bounds the *serialized* size: every node is charged an upper bound of what the serializer emits for it (escaped text, tag, attributes, a reserve for a minted id), so a tree inside `dom.Limits` can never make `document.Render` fail with `ErrLimit` and wedge painting (`dom.TestSerializedSizeNeverExceedsAccountedBytes`). When a page trips the heap or strike budget, the loop *drops its runtime*: `Sandbox.Close` nils the Goja runtime and every wrapper map, the page's requests are cancelled through its own context, timers and frames are cleared, and the GC runs once; the tree and last frame stay so links still open (`loop.TestDisabledPageIsDropped`, `jsvm.TestCloseDropsRuntime`). `debug.SetMemoryLimit` remains a soft GC target that one 50 ms slice can overshoot; it is the last layer, not a cap.
23. **Page loads are ordered by generation, not by goroutine luck.** `LoadPage` takes a generation synchronously on the caller, posts through `tryPost` and falls back to a goroutine only on a full inbox; the task no-ops unless its generation is still the latest, so a slow earlier load can never replace a newer page (`loop.TestLatestLoadPageWins` runs 50 rounds with a one-slot inbox). The UI's `current` URL and the VM's page therefore agree, which `fetch()` same-origin depends on.

## 6. Deliverable 1 — the sandbox (`jsvm/sandbox.go`)

What each hardening step defends against:

| Step | Threat |
| --- | --- |
| `parser.WithDisableSourceMaps` | `//# sourceMappingURL=/etc/passwd` → `os.ReadFile` inside the parser |
| `goja.Parse(..., WithDisableSourceMaps)` + `CompileAST` instead of `goja.Compile` | `goja.Compile` ignores runtime parser options and would reopen the row above |
| `SetMaxCallStackSize(512)` | recursion → multi-GB Go stack / crash |
| `time.AfterFunc(slice) → vm.Interrupt` + strikes → `disabled`; if the timer wins the race with `fn` returning, `Run` waits for the interrupt to land and clears it | `for(;;){}`, regex DoS, giant JSON; and a late interrupt striking the next innocent entry |
| `runtime/metrics` heap check after every entry → `disabled` over `HeapBudget` | allocation bombs that finish each slice in time |
| watchdog is pausable while the bridge runs a forced GC under budget pressure | Go-side work being billed to the script and producing false strikes |
| `SetTimeSource` (10 ms), `SetRandSource` | timing side channels, fingerprinting, non-reproducible replays |
| capture `JSON.parse`/`Promise` before scripts run | prototype pollution of host code paths |
| `Delete("eval")` | surface reduction (not a boundary: `Function` remains, compiles into the same sandbox) |
| `DefineDataProperty(..., writable=false, configurable=false)` for every host global | page swapping `fetch`/`document` with a Proxy that lies to later host calls |
| `recover()` in `Run` | a Go panic in a binding tearing down the loop |
| `SetPromiseRejectionTracker` | silent failures |

`jsvm/sandbox.go`

```go
// Package jsvm owns the Goja runtime for one page. It is the hardened
// boundary: this package imports neither os, net, net/http, os/exec nor
// syscall (enforced by TestNoSystemImports), exposes no Go value to scripts by
// reflection (every host object is built property-by-property), and runs every
// entry into JavaScript under a time-slice watchdog.
//
// Why Goja: pure Go, no CGO, bytecode interpreter with no JIT, builds for
// GOOS=js like nthtml. Its default runtime contains only ECMAScript built-ins:
// there is no require, process, fs, XMLHttpRequest or fetch unless the host
// adds them. Hardening is therefore mostly about what we add and how.
package jsvm

import (
	"errors"
	"fmt"
	"runtime/metrics"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"

	"nimbleterminal.dev/ntbrowser/dom"
)

// Config bounds one page's scripting. Zero fields use the defaults below.
type Config struct {
	Slice        time.Duration // wall-clock budget per entry into JS (default 50ms)
	Strikes      int           // slice overruns before scripting is disabled (default 3)
	MaxCallStack int           // default 512 frames
	MaxTimers    int           // live timers (default 256)
	MaxListeners int           // total event listeners (default 1024)
	HeapBudget   uint64        // process heap bytes after which this page is disabled (default 256 MiB)
	Clock        func() time.Time
	Rand         func() float64
	Location     string // read-only location.href shown to scripts
}

// Host is what the loop gives the sandbox. Every method is called on the VM
// goroutine. Completion callbacks the host receives must be invoked on the VM
// goroutine as well, never from a worker.
type Host interface {
	SetTimer(delay time.Duration, repeat bool, fn goja.Callable, args []goja.Value) (id int, ok bool)
	ClearTimer(id int)
	RequestFrame(fn goja.Callable) int
	CancelFrame(id int)
	// Fetch starts a request and later calls done exactly once on the VM goroutine.
	Fetch(req FetchRequest, done func(FetchResponse, error)) error
	Log(level string, line string)
}

type FetchRequest struct {
	Method, URL, Body string
	Headers           map[string]string
}

type FetchResponse struct {
	Status     int
	StatusText string
	URL        string
	Headers    map[string]string
	Body       string
}

type Sandbox struct {
	vm       *goja.Runtime
	cfg      Config
	host     Host
	tree     *dom.Tree
	strikes  int
	disabled bool
	running  bool

	elemProto *goja.Object
	nodeSym   *goja.Symbol // wrapper -> NodeID
	listSym   *goja.Symbol // wrapper -> {type: [listeners]}
	// weakW holds every wrapper weakly; strong pins listener-bearing wrappers
	// of attached nodes. See bindings.go for the lifetime rules.
	weakW  map[dom.NodeID]weak.Pointer[goja.Object]
	strong map[dom.NodeID]*goja.Object
	// listenCount[node][type] mirrors the callbacks stored on wrappers, as
	// plain integers, so budgets survive wrapper collection.
	listenCount map[dom.NodeID]map[string]int
	typeCount   map[string]int
	nListen     int
	active      dom.NodeID // focused element, 0 = none
	collectedMu sync.Mutex
	collected   []dom.NodeID // NodeIDs whose wrappers the GC reclaimed
	docObj      *goja.Object
	jsonParse   goja.Callable
	promiseCtor *goja.Object
	wd          watchdog
}

// watchdog is the time dimension of the sandbox: one timer per entry into
// JS that calls Runtime.Interrupt when the slice expires. It can be paused
// while the bridge does Go-side work on the page's behalf (forced GC under
// budget pressure) so that work is not billed to the script.
type watchdog struct {
	vm       *goja.Runtime
	timer    *time.Timer
	fired    chan struct{}
	deadline time.Time
}

func (w *watchdog) arm(d time.Duration) {
	w.fired = make(chan struct{})
	w.deadline = time.Now().Add(d)
	fired := w.fired
	w.timer = time.AfterFunc(d, func() { w.vm.Interrupt("time slice exceeded"); close(fired) })
}

// disarm stops the timer. If the timer won the race, it waits for Interrupt
// to land and clears it so a late interrupt cannot hit the next, innocent
// entry. It reports whether the slice had expired.
func (w *watchdog) disarm() (expired bool) {
	if w.timer == nil {
		return false
	}
	if !w.timer.Stop() {
		<-w.fired
		w.vm.ClearInterrupt()
		expired = true
	}
	w.timer = nil
	return expired
}

// pause stops the clock and returns a resume function that re-arms it with
// the time that was left (zero if it had already expired, which interrupts
// the script at its next instruction, as it should).
func (w *watchdog) pause() (resume func()) {
	remaining := time.Until(w.deadline)
	if w.disarm() || remaining < 0 {
		remaining = 0
	}
	return func() { w.arm(remaining) }
}

var ErrDisabled = errors.New("ntbrowser/jsvm: scripting disabled for this page")

// maxScriptBytes bounds one script source (parse work is roughly linear).
const maxScriptBytes = 1 << 20

func New(cfg Config, tree *dom.Tree, host Host) *Sandbox {
	if cfg.Slice <= 0 {
		cfg.Slice = 50 * time.Millisecond
	}
	if cfg.Strikes <= 0 {
		cfg.Strikes = 3
	}
	if cfg.MaxCallStack <= 0 {
		cfg.MaxCallStack = 512
	}
	if cfg.MaxTimers <= 0 {
		cfg.MaxTimers = 256
	}
	if cfg.MaxListeners <= 0 {
		cfg.MaxListeners = 1024
	}
	if cfg.HeapBudget == 0 {
		cfg.HeapBudget = 256 << 20
	}
	if cfg.Clock == nil {
		// Coarse clock: 10ms granularity blunts timing side channels and
		// makes Date.now() useless as a high-resolution profiler.
		cfg.Clock = func() time.Time { return time.Now().Truncate(10 * time.Millisecond) }
	}
	if cfg.Rand == nil {
		cfg.Rand = defaultRand
	}
	s := &Sandbox{cfg: cfg, host: host, tree: tree,
		weakW: map[dom.NodeID]weak.Pointer[goja.Object]{}, strong: map[dom.NodeID]*goja.Object{},
		listenCount: map[dom.NodeID]map[string]int{}, typeCount: map[string]int{}}
	s.vm = goja.New()
	s.wd.vm = s.vm
	s.harden()
	s.nodeSym = goja.NewSymbol("ntbrowser.node")
	s.listSym = goja.NewSymbol("ntbrowser.listeners")
	s.elemProto = s.buildElementProto()
	s.installGlobals()
	return s
}

// harden configures the interpreter itself, before any host API exists.
func (s *Sandbox) harden() {
	vm := s.vm
	// Goja's parser honours "//# sourceMappingURL=" by default and its default
	// loader reads the referenced path with os.ReadFile. Disabling source maps
	// removes the only filesystem touch point inside the engine.
	vm.SetParserOptions(parser.WithDisableSourceMaps)
	// Infinite recursion becomes a catchable StackOverflowError instead of a
	// multi-gigabyte Go stack.
	vm.SetMaxCallStackSize(s.cfg.MaxCallStack)
	// Deterministic, coarse, host-controlled time and randomness.
	vm.SetTimeSource(goja.Now(s.cfg.Clock))
	vm.SetRandSource(goja.RandSource(s.cfg.Rand))
	// Unhandled rejections surface in the terminal log instead of vanishing.
	vm.SetPromiseRejectionTracker(func(p *goja.Promise, op goja.PromiseRejectionOperation) {
		if op == goja.PromiseRejectionReject {
			s.host.Log("error", "Unhandled promise rejection: "+safeString(p.Result()))
		}
	})
	// Capture pristine built-ins before any script can monkey-patch them.
	s.jsonParse, _ = goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	s.promiseCtor = vm.Get("Promise").ToObject(vm)
	// Strip dynamic-code entry points that reader-mode content has no use
	// for. Direct eval is gone; the Function constructor remains reachable via
	// Function.prototype.constructor, but it compiles into the same sandbox,
	// so this is attack-surface reduction, not a security boundary.
	global := vm.GlobalObject()
	_ = global.Delete("eval")
}

// defineFrozen installs a host API as non-writable, non-configurable. Scripts
// cannot shadow fetch or document with their own implementations, and a
// Proxy-wrapped replacement cannot be swapped in later.
func defineFrozen(obj *goja.Object, name string, v goja.Value) {
	_ = obj.DefineDataProperty(name, v, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

func (s *Sandbox) installGlobals() {
	vm := s.vm
	global := vm.GlobalObject()
	defineFrozen(global, "window", global)
	defineFrozen(global, "self", global)
	defineFrozen(global, "console", s.consoleObject())
	defineFrozen(global, "document", s.documentObject())
	defineFrozen(global, "fetch", vm.ToValue(s.fetch))
	location := vm.NewObject()
	defineFrozen(location, "href", vm.ToValue(s.cfg.Location))
	defineFrozen(global, "location", location)
	navigator := vm.NewObject()
	defineFrozen(navigator, "userAgent", vm.ToValue("ntbrowser"))
	defineFrozen(global, "navigator", navigator)
	defineFrozen(global, "setTimeout", vm.ToValue(s.timerFunc(false)))
	defineFrozen(global, "setInterval", vm.ToValue(s.timerFunc(true)))
	defineFrozen(global, "clearTimeout", vm.ToValue(s.clearTimer))
	defineFrozen(global, "clearInterval", vm.ToValue(s.clearTimer))
	defineFrozen(global, "requestAnimationFrame", vm.ToValue(s.requestFrame))
	defineFrozen(global, "cancelAnimationFrame", vm.ToValue(s.cancelFrame))
}

// Run is the single entry point into JavaScript. It arms the watchdog, runs
// fn, and converts every failure mode into an error the loop can log.
// Goja drains the promise job queue when the outermost call returns, so
// microtasks are included in the slice.
func (s *Sandbox) Run(fn func(vm *goja.Runtime) error) (err error) {
	if s.disabled {
		return ErrDisabled
	}
	if s.running {
		return errors.New("ntbrowser/jsvm: re-entrant Run")
	}
	s.running = true
	s.reap() // apply GC reports before the page runs again
	s.vm.ClearInterrupt()
	s.wd.arm(s.cfg.Slice)
	defer func() {
		s.wd.disarm()
		s.running = false
		s.checkHeap()
		if r := recover(); r != nil {
			// A Go panic inside a binding must never take the loop down.
			err = fmt.Errorf("ntbrowser/jsvm: binding panic: %v", r)
			s.strike()
		}
	}()
	err = fn(s.vm)
	var interrupted *goja.InterruptedError
	var overflow *goja.StackOverflowError
	var exception *goja.Exception
	switch {
	case errors.As(err, &interrupted):
		s.strike()
		return fmt.Errorf("ntbrowser/jsvm: %v (strike %d/%d)", interrupted.Value(), s.strikes, s.cfg.Strikes)
	case errors.As(err, &overflow):
		s.strike()
		return err
	case errors.As(err, &exception):
		return fmt.Errorf("ntbrowser/jsvm: uncaught %s", safeString(exception.Value()))
	}
	return err
}

func (s *Sandbox) strike() {
	s.strikes++
	if s.strikes >= s.cfg.Strikes {
		s.disabled = true
		s.host.Log("error", "scripting disabled: repeated time slice overruns")
	}
}

func (s *Sandbox) Disabled() bool { return s.disabled }

// Close disables the page's scripting for good and drops every reference to
// the Goja runtime and the wrappers so the GC can reclaim the JS heap. The
// loop calls it when the heap budget or the strike budget is exhausted and
// when a page is replaced; after Close every entry point returns ErrDisabled
// and Dispatch reports "not prevented". The dom.Tree is left to its owner.
func (s *Sandbox) Close() {
	s.disabled = true
	s.wd.disarm()
	s.vm = nil
	s.wd.vm = nil
	s.elemProto, s.docObj, s.promiseCtor, s.jsonParse = nil, nil, nil, nil
	s.weakW = map[dom.NodeID]weak.Pointer[goja.Object]{}
	s.strong = map[dom.NodeID]*goja.Object{}
	s.listenCount = map[dom.NodeID]map[string]int{}
	s.typeCount = map[string]int{}
	s.nListen = 0
}

// Eval compiles and runs page script source in sloppy mode with a stable
// name for stack traces. Compile errors are returned, not thrown.
//
// It must NOT use goja.Compile: that package-level function parses with no
// parser options (runtime.go: compile(name, src, strict, true, nil)), so the
// WithDisableSourceMaps set on the runtime would be ignored and a
// "//# sourceMappingURL=/etc/passwd" comment would reach os.ReadFile.
// Parse with explicit options, then CompileAST.
func (s *Sandbox) Eval(name, src string) error {
	if len(src) > maxScriptBytes {
		return fmt.Errorf("ntbrowser/jsvm: script %s over %d bytes", name, maxScriptBytes)
	}
	ast, err := goja.Parse(name, src, sandboxParserOptions()...)
	if err != nil {
		return err
	}
	prg, err := goja.CompileAST(ast, false)
	if err != nil {
		return err
	}
	return s.Run(func(vm *goja.Runtime) error { _, err := vm.RunProgram(prg); return err })
}

// Call invokes a stored callback under the watchdog.
func (s *Sandbox) Call(fn goja.Callable, this goja.Value, args ...goja.Value) error {
	return s.Run(func(vm *goja.Runtime) error { _, err := fn(this, args...); return err })
}

// ---- timers and frames: thin shims over Host ----

func (s *Sandbox) timerFunc(repeat bool) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(s.vm.NewTypeError("callback must be a function; string callbacks are not supported"))
		}
		delay := time.Duration(call.Argument(1).ToFloat() * float64(time.Millisecond))
		if delay < 4*time.Millisecond && repeat {
			delay = 4 * time.Millisecond // browser clamp; prevents timer storms
		}
		if delay < 0 {
			delay = 0
		}
		var args []goja.Value
		if len(call.Arguments) > 2 {
			args = append(args, call.Arguments[2:]...)
		}
		id, ok := s.host.SetTimer(delay, repeat, fn, args)
		if !ok {
			panic(s.vm.NewGoError(errors.New("too many timers")))
		}
		return s.vm.ToValue(id)
	}
}

func (s *Sandbox) clearTimer(call goja.FunctionCall) goja.Value {
	s.host.ClearTimer(int(call.Argument(0).ToInteger()))
	return goja.Undefined()
}

func (s *Sandbox) requestFrame(call goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(s.vm.NewTypeError("callback must be a function"))
	}
	return s.vm.ToValue(s.host.RequestFrame(fn))
}

func (s *Sandbox) cancelFrame(call goja.FunctionCall) goja.Value {
	s.host.CancelFrame(int(call.Argument(0).ToInteger()))
	return goja.Undefined()
}

// ---- console: terminal-safe logging ----

func (s *Sandbox) consoleObject() *goja.Object {
	c := s.vm.NewObject()
	for _, level := range []string{"log", "info", "warn", "error", "debug"} {
		level := level
		defineFrozen(c, level, s.vm.ToValue(func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				parts = append(parts, safeString(a))
			}
			s.host.Log(level, strings.Join(parts, " "))
			return goja.Undefined()
		}))
	}
	return c
}

// safeString renders a JS value for a terminal: bounded length and no C0/C1
// control characters, so a page cannot inject escape sequences (OSC, DCS,
// cursor moves, Kitty graphics commands) into the host's status line or log.
func safeString(v goja.Value) string {
	if v == nil {
		return "undefined"
	}
	var str string
	if obj, ok := v.(*goja.Object); ok && obj.ClassName() != "Error" {
		if b, err := obj.MarshalJSON(); err == nil {
			str = string(b)
		} else {
			str = v.String()
		}
	} else {
		str = v.String()
	}
	const maxLog = 4096
	if len(str) > maxLog {
		str = str[:maxLog] + "…"
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
			if r == '\n' || r == '\t' {
				return ' '
			}
			return -1
		}
		return r
	}, str)
}

// defaultRand is intentionally not crypto/rand: scripts in reader mode do
// not get a CSPRNG, and the host can inject a seeded source for replay.
var randState uint64 = 0x9E3779B97F4A7C15

func defaultRand() float64 {
	randState ^= randState << 13
	randState ^= randState >> 7
	randState ^= randState << 17
	return float64(randState>>11) / float64(1<<53)
}

// ToValue converts plain Go scalars/strings for the loop (e.g. rAF
// timestamps). It must never be handed a struct or pointer: goja would
// reflect over it and expose Go fields to scripts.
func (s *Sandbox) ToValue(v any) goja.Value {
	switch v.(type) {
	case nil, bool, string, int, int64, float64:
		return s.vm.ToValue(v)
	}
	panic("jsvm: refusing to reflect a Go value into the sandbox")
}

// checkHeap is the memory dimension of the watchdog. Goja cannot cap its own
// heap, and a script can allocate hundreds of MB inside one 50 ms slice
// without ever tripping Interrupt. After every entry the live heap is read
// (runtime/metrics; cheap, no stop-the-world) and a page over budget is
// disabled; the loop then drops the page so the GC can reclaim it. This
// degrades the page instead of letting debug.SetMemoryLimit thrash or the
// process die, but a single pathological slice can still overshoot.
var heapSample = []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}

func (s *Sandbox) checkHeap() {
	metrics.Read(heapSample)
	if heapSample[0].Value.Kind() == metrics.KindUint64 && heapSample[0].Value.Uint64() > s.cfg.HeapBudget {
		s.disabled = true
		s.host.Log("error", "scripting disabled: heap budget exceeded")
	}
}

// sandboxParserOptions is the single definition of how page source is parsed.
func sandboxParserOptions() []parser.Option { return []parser.Option{parser.WithDisableSourceMaps} }

// Reap applies pending GC reports now (the loop calls it after runtime.GC
// when the node budget is hit; Run calls it on every entry).
func (s *Sandbox) Reap() { s.reap() }
```

### 6b. `fetch()` — binding and policy

The binding (`jsvm/fetch.go`) only builds plain strings and settles promises. All admission lives in `netpolicy`, which is also what page navigation uses, so there is exactly one answer to "may this URL be touched".

`jsvm/fetch.go`

```go
package jsvm

import (
	"errors"

	"github.com/dop251/goja"
)

// fetch(url, init?) returns a Promise. The sandbox never touches a socket:
// it builds a FetchRequest out of plain strings, hands it to Host.Fetch, and
// settles the promise when the host calls back on the VM goroutine. Only the
// subset of the Fetch API that reader-mode pages need is modelled: method,
// headers, body, and a Response with ok/status/headers/text()/json().
func (s *Sandbox) fetch(call goja.FunctionCall) goja.Value {
	vm := s.vm
	promise, resolve, reject := vm.NewPromise()
	req := FetchRequest{URL: call.Argument(0).String(), Method: "GET", Headers: map[string]string{}}
	if init, ok := call.Argument(1).(*goja.Object); ok {
		if m := init.Get("method"); m != nil && !goja.IsUndefined(m) {
			req.Method = m.String()
		}
		if b := init.Get("body"); b != nil && !goja.IsUndefined(b) && !goja.IsNull(b) {
			req.Body = b.String() // only string bodies; no Blob/FormData/streams
		}
		if h, ok := init.Get("headers").(*goja.Object); ok {
			for _, k := range h.Keys() {
				if len(req.Headers) >= 8 {
					break
				}
				req.Headers[k] = h.Get(k).String()
			}
		}
	}
	err := s.host.Fetch(req, func(resp FetchResponse, err error) {
		// Runs on the VM goroutine, outside any Run slice, so settle under one.
		_ = s.Run(func(vm *goja.Runtime) error {
			if err != nil {
				return reject(vm.NewGoError(err))
			}
			return resolve(s.responseObject(resp))
		})
	})
	if err != nil {
		_ = reject(vm.NewGoError(err))
	}
	return vm.ToValue(promise)
}

func (s *Sandbox) responseObject(r FetchResponse) *goja.Object {
	vm := s.vm
	o := vm.NewObject()
	defineFrozen(o, "ok", vm.ToValue(r.Status >= 200 && r.Status < 300))
	defineFrozen(o, "status", vm.ToValue(r.Status))
	defineFrozen(o, "statusText", vm.ToValue(r.StatusText))
	defineFrozen(o, "url", vm.ToValue(r.URL))
	headers := vm.NewObject()
	defineFrozen(headers, "get", vm.ToValue(func(c goja.FunctionCall) goja.Value {
		if v, ok := r.Headers[toLowerASCII(c.Argument(0).String())]; ok {
			return vm.ToValue(v)
		}
		return goja.Null()
	}))
	defineFrozen(o, "headers", headers)
	consumed := false
	body := func(parse bool) func(goja.FunctionCall) goja.Value {
		return func(goja.FunctionCall) goja.Value {
			p, resolve, reject := vm.NewPromise()
			if consumed {
				_ = reject(vm.NewTypeError("body already consumed"))
				return vm.ToValue(p)
			}
			consumed = true
			if !parse {
				_ = resolve(vm.ToValue(r.Body))
				return vm.ToValue(p)
			}
			// Use the JSON.parse captured at startup, not whatever the page
			// has since assigned to globalThis.JSON.parse.
			v, err := s.jsonParse(goja.Undefined(), vm.ToValue(r.Body))
			if err != nil {
				_ = reject(errValue(vm, err))
			} else {
				_ = resolve(v)
			}
			return vm.ToValue(p)
		}
	}
	defineFrozen(o, "text", vm.ToValue(body(false)))
	defineFrozen(o, "json", vm.ToValue(body(true)))
	return o
}

func errValue(vm *goja.Runtime, err error) goja.Value {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		return ex.Value()
	}
	return vm.NewGoError(err)
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
```

`netpolicy/policy.go`

```go
// Package netpolicy is the only place in ntbrowser that opens sockets on behalf
// of a page. JavaScript never sees it: jsvm hands it a Request through the
// loop and gets back a Response struct, both plain data. Everything a browser
// would call CORS, mixed-content, SSRF or cookie policy lives here.
package netpolicy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Policy is fixed per page load. The zero value denies everything, so a
// misconfigured host fails closed.
type Policy struct {
	// Origin is the page's own URL. Same-origin requests are allowed when it
	// is set; relative URLs resolve against it.
	Origin *url.URL
	// AllowHosts lists additional hostnames. A leading dot allows subdomains.
	AllowHosts []string
	// AllowPrivate permits loopback/RFC1918 targets (development only).
	AllowPrivate bool
	// AnyOrigin admits every http(s) host: the policy for top-level
	// navigation under -nav free/confirm (decision 11), never for a page's
	// fetch(). Scheme, credentials, local names, literal and resolved
	// addresses (isForbiddenIP), cookies, redirects and body caps are
	// enforced exactly as without it.
	AnyOrigin    bool
	MaxBody      int64         // response bytes (default 2 MiB)
	Timeout      time.Duration // whole request (default 10s)
	MaxInFlight  int           // concurrent requests per page (default 4)
	MaxRedirects int           // default 3
}

type Request struct {
	Method  string
	URL     string
	Headers map[string]string // filtered against requestHeaderAllow
	Body    string
}

type Response struct {
	Status     int
	StatusText string
	URL        string // final URL after vetted redirects
	Headers    map[string]string
	Body       string
}

var (
	ErrDenied   = errors.New("ntbrowser/netpolicy: request denied by policy")
	ErrTooLarge = errors.New("ntbrowser/netpolicy: response too large")
	ErrBusy     = errors.New("ntbrowser/netpolicy: too many requests in flight")
)

var requestHeaderAllow = map[string]bool{"accept": true, "accept-language": true, "content-type": true}
var responseHeaderAllow = []string{"Content-Type", "Content-Length", "Last-Modified", "Etag", "Cache-Control"}

type Fetcher struct {
	p      Policy
	client *http.Client
	sem    chan struct{}
}

func New(p Policy) *Fetcher {
	if p.MaxBody <= 0 {
		p.MaxBody = 2 << 20
	}
	if p.Timeout <= 0 {
		p.Timeout = 10 * time.Second
	}
	if p.MaxInFlight <= 0 {
		p.MaxInFlight = 4
	}
	if p.MaxRedirects <= 0 {
		p.MaxRedirects = 3
	}
	f := &Fetcher{p: p, sem: make(chan struct{}, p.MaxInFlight)}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil, // never consult HTTP_PROXY from the page's requests
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: p.Timeout,
		// Resolve, vet every address, then dial the vetted IP. Vetting the
		// hostname alone would allow DNS rebinding between check and connect.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !f.p.AllowPrivate && isForbiddenIP(net.IP(ip.Unmap().AsSlice())) {
					return nil, fmt.Errorf("%w: %s resolves to %s", ErrDenied, host, ip)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	f.client = &http.Client{
		Transport: transport,
		Timeout:   p.Timeout,
		Jar:       nil, // no cookies, ever
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= f.p.MaxRedirects {
				return fmt.Errorf("%w: too many redirects", ErrDenied)
			}
			return f.Allowed(req.URL) // every hop re-enters policy
		},
	}
	return f
}

// Allowed is the single admission rule, shared by fetch() and navigation.
func (f *Fetcher) Allowed(u *url.URL) error {
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("%w: scheme %q", ErrDenied, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in URL", ErrDenied)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		if !f.p.AllowPrivate {
			return fmt.Errorf("%w: host %q", ErrDenied, host)
		}
	}
	if ip := net.ParseIP(host); ip != nil && !f.p.AllowPrivate && isForbiddenIP(ip) {
		return fmt.Errorf("%w: literal address %s", ErrDenied, host)
	}
	if f.p.AnyOrigin {
		return nil
	}
	// Same origin means scheme + host + port, as in browsers.
	if f.p.Origin != nil && f.p.Origin.Scheme == u.Scheme && strings.EqualFold(hostPort(f.p.Origin), hostPort(u)) {
		return nil
	}
	for _, a := range f.p.AllowHosts {
		a = strings.ToLower(a)
		if a == host || (strings.HasPrefix(a, ".") && strings.HasSuffix(host, a)) {
			return nil
		}
	}
	return fmt.Errorf("%w: host %q is not allowlisted", ErrDenied, host)
}

// Resolve turns a page-supplied reference into an admitted absolute URL.
func (f *Fetcher) Resolve(ref string) (*url.URL, error) {
	if len(ref) > 2048 || strings.ContainsAny(ref, "\x00\r\n\t ") {
		return nil, fmt.Errorf("%w: malformed reference", ErrDenied)
	}
	u, err := url.Parse(ref)
	if err != nil {
		return nil, err
	}
	if f.p.Origin != nil {
		u = f.p.Origin.ResolveReference(u)
	}
	u.Fragment = ""
	if err := f.Allowed(u); err != nil {
		return nil, err
	}
	return u, nil
}

// Do performs one bounded request. It is safe for concurrent use; the loop
// calls it from a worker goroutine, never from the VM goroutine.
func (f *Fetcher) Do(ctx context.Context, r Request) (Response, error) {
	select {
	case f.sem <- struct{}{}:
		defer func() { <-f.sem }()
	default:
		return Response{}, ErrBusy
	}
	u, err := f.Resolve(r.URL)
	if err != nil {
		return Response{}, err
	}
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return Response{}, fmt.Errorf("%w: method %s", ErrDenied, method)
	}
	if int64(len(r.Body)) > f.p.MaxBody {
		return Response{}, ErrTooLarge
	}
	ctx, cancel := context.WithTimeout(ctx, f.p.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(r.Body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("User-Agent", "ntbrowser/0.1 (+https://nimbleterminal.dev)")
	req.Header.Set("Accept", "text/*, application/json;q=0.9")
	for k, v := range r.Headers {
		if requestHeaderAllow[strings.ToLower(k)] && len(v) <= 1024 && !strings.ContainsAny(v, "\r\n") {
			req.Header.Set(k, v)
		}
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	if err := acceptableContentType(resp.Header.Get("Content-Type")); err != nil {
		return Response{}, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.p.MaxBody+1))
	if err != nil {
		return Response{}, err
	}
	if int64(len(body)) > f.p.MaxBody {
		return Response{}, ErrTooLarge
	}
	out := Response{Status: resp.StatusCode, StatusText: http.StatusText(resp.StatusCode), URL: resp.Request.URL.String(), Headers: map[string]string{}, Body: string(body)}
	for _, h := range responseHeaderAllow {
		if v := resp.Header.Get(h); v != "" {
			out.Headers[strings.ToLower(h)] = v
		}
	}
	return out, nil
}

func acceptableContentType(ct string) error {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return fmt.Errorf("%w: unreadable content type", ErrDenied)
	}
	switch {
	case strings.HasPrefix(mt, "text/"), mt == "application/json", strings.HasSuffix(mt, "+json"), mt == "application/xml", mt == "application/xhtml+xml", mt == "application/javascript", mt == "application/ecmascript":
		return nil
	}
	return fmt.Errorf("%w: content type %q", ErrDenied, mt)
}

// isForbiddenIP rejects everything that is not a public unicast address:
// loopback, RFC1918, link-local (169.254/16 holds cloud metadata), CGNAT,
// multicast, unspecified, and the IPv6 transition encodings that embed an
// IPv4 address a resolver or a page can choose (NAT64 64:ff9b::/96 and
// 64:ff9b:1::/48, 6to4 2002::/16, Teredo 2001::/32, IPv4-compatible ::/96).
// A DNS64 answer for an allowlisted name can embed 169.254.169.254; it is
// vetted through its embedded address, as is 6to4. Teredo and the local-use
// NAT64 range are rejected outright. Only IPv4-mapped addresses are unmapped
// first (netip.Addr.Unmap), so no mapped form can skip a case.
func isForbiddenIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	return forbiddenAddr(a.Unmap(), 0)
}

var (
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local     = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFour      = netip.MustParsePrefix("2002::/16")
	teredo         = netip.MustParsePrefix("2001::/32")
	v4Compatible   = netip.MustParsePrefix("::/96")
	cgnat          = netip.MustParsePrefix("100.64.0.0/10")
	thisNetwork    = netip.MustParsePrefix("0.0.0.0/8")
	ietfProtocol   = netip.MustParsePrefix("192.0.0.0/24")
)

func forbiddenAddr(a netip.Addr, depth int) bool {
	if depth > 1 {
		return true
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() || !a.IsValid() {
		return true
	}
	if a.Is4() {
		return cgnat.Contains(a) || thisNetwork.Contains(a) || ietfProtocol.Contains(a)
	}
	switch {
	case nat64Local.Contains(a), teredo.Contains(a):
		return true
	case nat64WellKnown.Contains(a), v4Compatible.Contains(a):
		b := a.As16()
		return forbiddenAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), depth+1)
	case sixToFour.Contains(a):
		b := a.As16()
		return forbiddenAddr(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), depth+1)
	}
	return false
}

func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Hostname() + ":" + port
}
```

## 7. Deliverable 2 — the proxy bridge

### 7a. The mutable tree (`dom/dom.go`)

`dom.Limits` has five dimensions: nodes, depth, bytes per text node or attribute, attributes per element, and `TotalBytes`, an upper bound of the serialized HTML (decision 22). Every parentless non-root node sits in `detached` until attached or released (decision 21).

`dom/dom.go`

```go
// Package dom is ntbrowser's own mutable document tree. JavaScript never sees
// these structs: the jsvm package hands scripts integer handles and proxies
// every operation through Tree methods. nthtml's document.Document is
// immutable, so a committed Tree is serialized to HTML and re-parsed by nthtml
// on every frame; this package is the single writable model.
package dom

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Limits bound what scripts can grow the tree into. Zero fields use defaults.
// They are deliberately at or below nthtml's document.DefaultLimits so a
// serialized tree can never exceed the renderer's own budget: Nodes and Depth
// stay under nthtml's, and TotalBytes bounds the *serialized* size (escaped
// text, tags, attributes and minted ids), so a tree inside these limits never
// makes document.Render fail with ErrLimit. The per-node TextBytes cap alone
// would not do that (8000 × 64 KiB is far over nthtml's 1 MiB input cap).
type Limits struct {
	Nodes      int // total live nodes (default 8000; nthtml caps parsing at 10000)
	Depth      int // nesting depth (default 48; nthtml caps at 64)
	TextBytes  int // bytes per text node or attribute value (default 64 KiB)
	Attrs      int // attributes per element (default 32)
	TotalBytes int // upper bound of Serialize() output (default 1 MiB = nthtml DefaultLimits.Bytes)
}

var DefaultLimits = Limits{Nodes: 8000, Depth: 48, TextBytes: 64 << 10, Attrs: 32, TotalBytes: 1 << 20}

// Serialized-size accounting. Every cost is an upper bound of what write()
// emits for that node, so Σ cost ≥ len(Serialize()). Elements reserve room
// for a minted ` id="nt-h-N"` (≤ 20 bytes for ids under 10^8).
const elementOverhead = 5 + 20 // '<' + '>' + '</' + '>' + minted id reserve

func textCost(s string) int           { return len(html.EscapeString(s)) }
func elementCost(tag string) int      { return 2*len(tag) + elementOverhead }
func attrCost(name, value string) int { return len(name) + 4 + textCost(value) } // ' name="value"'

func nodeCost(n *Node) int {
	if n.kind == Text {
		return textCost(n.text)
	}
	c := elementCost(n.tag)
	for _, a := range n.attrs {
		c += attrCost(a.Name, a.Value)
	}
	return c
}

var ErrLimit = errors.New("ntbrowser/dom: resource limit exceeded")

// NodeID is the only thing JavaScript ever holds. IDs are never reused within
// one Tree, so a stale handle fails closed instead of aliasing a new node.
type NodeID int

type Kind uint8

const (
	Element Kind = iota
	Text
)

type Node struct {
	id       NodeID
	kind     Kind
	tag      string // lower-case; empty for text nodes
	text     string // text nodes only
	attrs    []Attr // ordered, unique keys, lower-case names
	parent   *Node
	children []*Node
	// listeners is the set of event types JS registered on this node. The
	// serializer gives such elements a stable id attribute so nthtml's
	// Box.Anchor / Result.Anchors can be mapped back to a NodeID on hit test.
	listeners map[string]struct{}
}

type Attr struct{ Name, Value string }

func (n *Node) ID() NodeID        { return n.id }
func (n *Node) Kind() Kind        { return n.kind }
func (n *Node) Tag() string       { return n.tag }
func (n *Node) Text() string      { return n.text }
func (n *Node) Parent() *Node     { return n.parent }
func (n *Node) Children() []*Node { return append([]*Node(nil), n.children...) }

func (n *Node) Attr(name string) (string, bool) {
	name = strings.ToLower(name)
	for _, a := range n.attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// HasListener reports whether any JS listener of the given type is attached.
func (n *Node) HasListener(typ string) bool {
	_, ok := n.listeners[typ]
	return ok
}

// Tree is single-owner: only the VM goroutine mutates it. Serialize produces
// an immutable snapshot string that may be rendered on another goroutine.
type Tree struct {
	limits Limits
	root   *Node // synthetic "document" node
	nodes  map[NodeID]*Node
	// detached holds every parentless non-root node: subtree roots removed
	// from the document and nodes created but not (yet) inserted. The bridge
	// releases them once no wrapper in the subtree is alive; see Release.
	detached map[NodeID]*Node
	nextID   NodeID
	live     int
	bytes    int // Σ nodeCost over live nodes; ≥ len(Serialize())
	dirty    bool
	title    string
	// Scripts collected during Parse, in document order. They are stripped
	// from the serialized HTML so nthtml never sees them (defense in depth;
	// nthtml also never executes scripts).
	Scripts []Script
}

type Script struct {
	Inline string // inline source; empty when Src is set
	Src    string // external src as written; the host decides whether to fetch it
}

func limits(l Limits) Limits {
	d := DefaultLimits
	if l.Nodes > 0 && l.Nodes < d.Nodes {
		d.Nodes = l.Nodes
	}
	if l.Depth > 0 && l.Depth < d.Depth {
		d.Depth = l.Depth
	}
	if l.TextBytes > 0 && l.TextBytes < d.TextBytes {
		d.TextBytes = l.TextBytes
	}
	if l.Attrs > 0 && l.Attrs < d.Attrs {
		d.Attrs = l.Attrs
	}
	if l.TotalBytes > 0 && l.TotalBytes < d.TotalBytes {
		d.TotalBytes = l.TotalBytes
	}
	return d // values above defaults are ignored, mirroring nthtml's policy
}

// Parse builds a Tree from HTML using golang.org/x/net/html (already a
// dependency of nthtml). Script bodies are collected into t.Scripts and are
// not part of the tree. Input must already be bounded by the caller.
func Parse(r io.Reader, l Limits) (*Tree, error) {
	t := &Tree{limits: limits(l), nodes: map[NodeID]*Node{}, detached: map[NodeID]*Node{}}
	t.root = t.newNode(Element, "#document")
	delete(t.detached, t.root.id)
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	if err := t.ingest(t.root, doc, 0); err != nil {
		return nil, err
	}
	t.dirty = true
	return t, nil
}

func (t *Tree) ingest(parent *Node, src *html.Node, depth int) error {
	for c := src.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.DoctypeNode, html.CommentNode:
			continue
		case html.TextNode:
			if strings.TrimSpace(c.Data) == "" && parent.tag != "pre" {
				if parent.tag == "#document" || parent.tag == "html" || parent.tag == "head" {
					continue
				}
			}
			n, err := t.NewText(c.Data)
			if err != nil {
				return err
			}
			t.attach(parent, n)
		case html.ElementNode:
			switch c.DataAtom {
			case atom.Script:
				s := Script{}
				for _, a := range c.Attr {
					if strings.EqualFold(a.Key, "src") {
						s.Src = a.Val
					}
					if strings.EqualFold(a.Key, "type") && !isJSType(a.Val) {
						s.Src, s.Inline = "", "" // JSON/template blocks are not code
						goto skip
					}
				}
				if s.Src == "" && c.FirstChild != nil {
					s.Inline = c.FirstChild.Data
				}
				t.Scripts = append(t.Scripts, s)
			skip:
				continue
			case atom.Title:
				if c.FirstChild != nil {
					t.title = c.FirstChild.Data
				}
			}
			if depth+1 > t.limits.Depth {
				return fmt.Errorf("%w: depth %d", ErrLimit, t.limits.Depth)
			}
			n, err := t.NewElement(c.Data)
			if err != nil {
				return err
			}
			for _, a := range c.Attr {
				if err := t.SetAttr(n, a.Key, a.Val); err != nil {
					return err
				}
			}
			t.attach(parent, n)
			if err := t.ingest(n, c, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func isJSType(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "text/javascript", "application/javascript", "module", "text/ecmascript":
		return true
	}
	return false
}

func (t *Tree) newNode(k Kind, tag string) *Node {
	t.nextID++
	n := &Node{id: t.nextID, kind: k, tag: tag}
	t.nodes[n.id] = n
	t.detached[n.id] = n // parentless until attached
	return n
}

// charge reserves delta serialized bytes or fails with ErrLimit.
func (t *Tree) charge(delta int) error {
	if delta > 0 && t.bytes+delta > t.limits.TotalBytes {
		return fmt.Errorf("%w: serialized size over %d bytes", ErrLimit, t.limits.TotalBytes)
	}
	t.bytes += delta
	return nil
}

// ---- mutation API: the only entry points the JS bridge is allowed to call ----

func (t *Tree) Root() *Node          { return t.root }
func (t *Tree) Title() string        { return t.title }
func (t *Tree) Dirty() bool          { return t.dirty }
func (t *Tree) ClearDirty()          { t.dirty = false }
func (t *Tree) Node(id NodeID) *Node { return t.nodes[id] }

func (t *Tree) Body() *Node {
	return t.find(t.root, func(n *Node) bool { return n.tag == "body" })
}

func (t *Tree) NewElement(tag string) (*Node, error) {
	tag = strings.ToLower(tag)
	if tag == "" || tag == "script" || tag == "#document" || !validName(tag) {
		return nil, fmt.Errorf("ntbrowser/dom: invalid tag %q", tag)
	}
	if t.live+1 > t.limits.Nodes {
		return nil, fmt.Errorf("%w: %d nodes", ErrLimit, t.limits.Nodes)
	}
	if err := t.charge(elementCost(tag)); err != nil {
		return nil, err
	}
	t.live++
	return t.newNode(Element, tag), nil
}

func (t *Tree) NewText(s string) (*Node, error) {
	if len(s) > t.limits.TextBytes {
		return nil, fmt.Errorf("%w: text over %d bytes", ErrLimit, t.limits.TextBytes)
	}
	if t.live+1 > t.limits.Nodes {
		return nil, fmt.Errorf("%w: %d nodes", ErrLimit, t.limits.Nodes)
	}
	if err := t.charge(textCost(s)); err != nil {
		return nil, err
	}
	t.live++
	n := t.newNode(Text, "")
	n.text = s
	return n, nil
}

func (t *Tree) SetAttr(n *Node, name, value string) error {
	name = strings.ToLower(name)
	if n.kind != Element || !validName(name) {
		return errors.New("ntbrowser/dom: invalid attribute name")
	}
	if len(value) > t.limits.TextBytes {
		return fmt.Errorf("%w: attribute over %d bytes", ErrLimit, t.limits.TextBytes)
	}
	// Event-handler attributes are inert: the only way to run code is
	// addEventListener through the bridge, which the loop controls.
	if strings.HasPrefix(name, "on") {
		return nil
	}
	for i := range n.attrs {
		if n.attrs[i].Name == name {
			if err := t.charge(textCost(value) - textCost(n.attrs[i].Value)); err != nil {
				return err
			}
			n.attrs[i].Value = value
			t.dirty = true
			return nil
		}
	}
	if len(n.attrs) >= t.limits.Attrs {
		return fmt.Errorf("%w: %d attributes", ErrLimit, t.limits.Attrs)
	}
	if err := t.charge(attrCost(name, value)); err != nil {
		return err
	}
	n.attrs = append(n.attrs, Attr{name, value})
	t.dirty = true
	return nil
}

func (t *Tree) RemoveAttr(n *Node, name string) {
	name = strings.ToLower(name)
	for i := range n.attrs {
		if n.attrs[i].Name == name {
			t.bytes -= attrCost(n.attrs[i].Name, n.attrs[i].Value)
			n.attrs = append(n.attrs[:i], n.attrs[i+1:]...)
			t.dirty = true
			return
		}
	}
}

func (t *Tree) SetText(n *Node, s string) error {
	if len(s) > t.limits.TextBytes {
		return fmt.Errorf("%w: text over %d bytes", ErrLimit, t.limits.TextBytes)
	}
	switch n.kind {
	case Text:
		if err := t.charge(textCost(s) - textCost(n.text)); err != nil {
			return err
		}
		n.text = s
	case Element: // textContent setter semantics: replace all children
		// Allocate first so a failed write leaves the children in place
		// (the bridge may retry after a GC). An empty string just clears, as
		// in browsers, and costs no node.
		var c *Node
		if s != "" {
			var err error
			if c, err = t.NewText(s); err != nil {
				return err
			}
		}
		for _, old := range n.children {
			old.parent = nil
			t.detached[old.id] = old // the bridge releases them once unreferenced
		}
		n.children = nil
		if c != nil {
			t.attach(n, c)
		}
	}
	t.dirty = true
	return nil
}

// TextContent concatenates descendant text, like the DOM getter.
func TextContent(n *Node) string {
	var b strings.Builder
	var walk func(*Node)
	walk = func(n *Node) {
		if n.kind == Text {
			b.WriteString(n.text)
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func (t *Tree) Append(parent, child *Node) error {
	return t.InsertBefore(parent, child, nil)
}

func (t *Tree) InsertBefore(parent, child, ref *Node) error {
	if parent.kind != Element || parent == child {
		return errors.New("ntbrowser/dom: invalid parent")
	}
	for a := parent; a != nil; a = a.parent {
		if a == child {
			return errors.New("ntbrowser/dom: cycle")
		}
	}
	if depthOf(parent)+height(child) > t.limits.Depth {
		return fmt.Errorf("%w: depth %d", ErrLimit, t.limits.Depth)
	}
	if ref != nil && ref.parent != parent {
		return errors.New("ntbrowser/dom: reference node is not a child")
	}
	if ref == child {
		return nil // insertBefore(node, node) is a move to its own place: no-op
	}
	if child.parent != nil {
		t.detach(child)
	}
	if ref == nil {
		t.attach(parent, child)
		t.dirty = true
		return nil
	}
	for i, c := range parent.children {
		if c == ref {
			parent.children = append(parent.children[:i], append([]*Node{child}, parent.children[i:]...)...)
			child.parent = parent
			delete(t.detached, child.id)
			t.dirty = true
			return nil
		}
	}
	// Unreachable after the checks above; if it ever happens the child is
	// parentless and must stay reapable.
	t.detached[child.id] = child
	return errors.New("ntbrowser/dom: reference node is not a child")
}

// Detach removes the subtree from its parent but keeps it alive: browser
// semantics, where a removed node stays usable through JS references and can
// be re-inserted. Detached roots count toward the node budget until Release.
func (t *Tree) Detach(n *Node) {
	if n == t.root || n.parent == nil {
		return
	}
	t.detach(n)
	t.detached[n.id] = n
	t.dirty = true
}

// Release frees a detached subtree. The bridge calls it when no JS wrapper
// for any node in the subtree is alive any more (or never existed).
func (t *Tree) Release(n *Node) {
	if n.parent != nil || n == t.root {
		return // attached nodes are never released
	}
	delete(t.detached, n.id)
	t.release(n)
}

// Attached reports whether n is reachable from the document root.
func (t *Tree) Attached(n *Node) bool { return t.RootOf(n) == t.root }

// RootOf returns the topmost ancestor: the document root, or a detached root.
func (t *Tree) RootOf(n *Node) *Node {
	for n.parent != nil {
		n = n.parent
	}
	return n
}

// Live is the number of nodes counted against the budget, attached or not.
func (t *Tree) Live() int { return t.live }

// Walk visits n and its descendants in document order.
func Walk(n *Node, fn func(*Node)) {
	fn(n)
	for _, c := range n.children {
		Walk(c, fn)
	}
}

func (t *Tree) AddListener(n *Node, typ string) {
	if n.listeners == nil {
		n.listeners = map[string]struct{}{}
	}
	if _, had := n.listeners[typ]; !had {
		n.listeners[typ] = struct{}{}
		// An element that can be clicked needs an anchor name in the raster.
		if _, hasID := n.Attr("id"); !hasID && n.kind == Element {
			t.dirty = true
		}
	}
}

func (t *Tree) RemoveListener(n *Node, typ string) { delete(n.listeners, typ) }

// ByID resolves an id attribute (including synthetic ones from Serialize).
func (t *Tree) ByID(id string) *Node {
	if n, ok := parseSynthetic(id); ok {
		return t.nodes[n]
	}
	return t.find(t.root, func(n *Node) bool { v, ok := n.Attr("id"); return ok && v == id })
}

// Query supports the selector subset the bridge exposes: tag, #id, .class and
// descendant combinations of those. Anything richer throws in JS.
func (t *Tree) Query(from *Node, selector string, all bool) ([]*Node, error) {
	parts := strings.Fields(selector)
	if len(parts) == 0 || len(parts) > 8 {
		return nil, errors.New("ntbrowser/dom: unsupported selector")
	}
	preds := make([]func(*Node) bool, 0, len(parts))
	for _, p := range parts {
		pred, err := simplePredicate(p)
		if err != nil {
			return nil, err
		}
		preds = append(preds, pred)
	}
	var out []*Node
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		for _, c := range n.children {
			if c.kind == Element && preds[len(preds)-1](c) && ancestorsMatch(c, preds[:len(preds)-1], from) {
				out = append(out, c)
				if !all {
					return true
				}
			}
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(from)
	return out, nil
}

func ancestorsMatch(n *Node, preds []func(*Node) bool, stop *Node) bool {
	i := len(preds) - 1
	for a := n.parent; a != nil && a != stop && i >= 0; a = a.parent {
		if preds[i](a) {
			i--
		}
	}
	return i < 0
}

func simplePredicate(p string) (func(*Node) bool, error) {
	switch {
	case strings.HasPrefix(p, "#"):
		id := p[1:]
		return func(n *Node) bool { v, ok := n.Attr("id"); return ok && v == id }, nil
	case strings.HasPrefix(p, "."):
		cls := p[1:]
		return func(n *Node) bool { return hasClass(n, cls) }, nil
	case p == "*":
		return func(*Node) bool { return true }, nil
	case validName(p):
		tag := strings.ToLower(p)
		return func(n *Node) bool { return n.tag == tag }, nil
	}
	return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
}

func hasClass(n *Node, cls string) bool {
	v, _ := n.Attr("class")
	for _, c := range strings.Fields(v) {
		if c == cls {
			return true
		}
	}
	return false
}

// ---- serialization: the frame handed to nthtml ----

// Serialize writes the tree as HTML that nthtml's document.Parse accepts.
// <script> never appears (none are in the tree). Elements with listeners
// but no id get a synthetic id so nthtml's Result.Anchors / Box.Anchor can
// be mapped back to NodeIDs for hit testing. Text and attribute values are
// escaped by x/net/html, so a script cannot smuggle markup through
// textContent or setAttribute.
func (t *Tree) Serialize() string {
	var b strings.Builder
	t.write(&b, t.root)
	return b.String()
}

func (t *Tree) write(b *strings.Builder, n *Node) {
	switch n.kind {
	case Text:
		if n.parent != nil && (n.parent.tag == "style") {
			b.WriteString(sanitizeCSS(n.text)) // style text is raw, not escaped
			return
		}
		b.WriteString(html.EscapeString(n.text))
		return
	case Element:
		if n.tag != "#document" {
			b.WriteByte('<')
			b.WriteString(n.tag)
			hasID := false
			for _, a := range n.attrs {
				if a.Name == "id" {
					hasID = true
				}
				b.WriteByte(' ')
				b.WriteString(a.Name)
				b.WriteString(`="`)
				b.WriteString(html.EscapeString(a.Value))
				b.WriteByte('"')
			}
			if !hasID && len(n.listeners) > 0 {
				fmt.Fprintf(b, ` id="%s"`, syntheticID(n.id))
			}
			b.WriteByte('>')
		}
		for _, c := range n.children {
			t.write(b, c)
		}
		if n.tag != "#document" && !voidElement(n.tag) {
			b.WriteString("</")
			b.WriteString(n.tag)
			b.WriteByte('>')
		}
	}
}

// sanitizeCSS only prevents a </style> break-out; nthtml's own CSS parser
// enforces rule budgets and ignores url() resources.
func sanitizeCSS(s string) string { return strings.ReplaceAll(s, "</", "<\\/") }

const syntheticPrefix = "nt-h-"

func syntheticID(id NodeID) string { return fmt.Sprintf("%s%d", syntheticPrefix, int(id)) }

func parseSynthetic(s string) (NodeID, bool) {
	if !strings.HasPrefix(s, syntheticPrefix) {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(s[len(syntheticPrefix):], "%d", &n); err != nil {
		return 0, false
	}
	return NodeID(n), true
}

// ---- internals ----

func (t *Tree) attach(p, c *Node) {
	c.parent = p
	p.children = append(p.children, c)
	delete(t.detached, c.id)
}

func (t *Tree) detach(c *Node) {
	p := c.parent
	for i, s := range p.children {
		if s == c {
			p.children = append(p.children[:i], p.children[i+1:]...)
			break
		}
	}
	c.parent = nil
}

func (t *Tree) release(n *Node) {
	delete(t.nodes, n.id)
	delete(t.detached, n.id)
	t.live--
	t.bytes -= nodeCost(n)
	for _, c := range n.children {
		t.release(c)
	}
}

// Bytes is the accounted upper bound of len(Serialize()).
func (t *Tree) Bytes() int { return t.bytes }

func (t *Tree) find(from *Node, pred func(*Node) bool) *Node {
	if pred(from) {
		return from
	}
	for _, c := range from.children {
		if r := t.find(c, pred); r != nil {
			return r
		}
	}
	return nil
}

func depthOf(n *Node) int {
	d := 0
	for ; n != nil; n = n.parent {
		d++
	}
	return d
}

func height(n *Node) int {
	h := 0
	for _, c := range n.children {
		h = max(h, height(c))
	}
	return h + 1
}

func validName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '_' || r == ':'):
		default:
			return false
		}
	}
	return true
}

func voidElement(tag string) bool {
	switch tag {
	case "br", "hr", "img", "meta", "link", "input", "col", "wbr":
		return true
	}
	return false
}

// DetachedRoots lists subtree roots removed from the document and not released.
func (t *Tree) DetachedRoots() []*Node {
	out := make([]*Node, 0, len(t.detached))
	for _, n := range t.detached {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// Listeners lists nodes that registered a listener of typ, in document order.
// The loop uses it to decide whether a terminal event is worth forwarding.
func (t *Tree) Listeners(typ string) []*Node {
	var out []*Node
	for _, n := range t.nodes {
		if n.HasListener(typ) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}
```

### 7b. Bindings (`jsvm/bindings.go`)

Exposed surface — `document`: `getElementById`, `querySelector(All)` (tag, `#id`, `.class`, descendant chains), `createElement`, `createTextNode`, `body`, `documentElement`, `activeElement`, `title`, `add/removeEventListener`. Elements: `tagName`, `nodeType`, `isConnected`, `id`, `className`, `classList`, `textContent`/`innerText`, `parentElement`, `parentNode`, `firstChild`, `childNodes`, `children`, `get/set/has/removeAttribute`, `appendChild`, `insertBefore`, `removeChild`, `remove`, `querySelector(All)`, `add/removeEventListener`, `focus`, `blur`. Events: `type`, `target`, `currentTarget`, `key`, `href`, `preventDefault`, `stopPropagation`, `defaultPrevented`. `on*` attributes are inert. Lifetime rules are in the header comment of `bindings.go` and decision 12.

`jsvm/bindings.go`

```go
package jsvm

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
	"time"
	"weak"

	"github.com/dop251/goja"

	"nimbleterminal.dev/ntbrowser/dom"
)

// Element wrappers are ordinary JS objects whose prototype carries the
// methods. The only state on a wrapper is a symbol-keyed integer NodeID and
// a symbol-keyed bag of event listeners. Every method resolves the ID through
// the Tree on each call, so no Go pointer is ever reachable from JavaScript
// (ToValue is never called on a *dom.Node or any struct).
//
// Lifetime (browser semantics, arbitrary pages): a removed node stays alive
// while JavaScript references it and can be re-inserted. The bridge holds
// wrappers in two maps:
//
//   - weak:   every wrapper, as a weak.Pointer. JS unreachability is Go
//             unreachability in Goja, so the GC collects dropped wrappers
//             and runtime.AddCleanup reports the NodeID to the sandbox.
//   - strong: wrappers of *attached* nodes that carry listeners. A listener
//             on an attached element must keep firing even when the page
//             forgot the element, so those cannot be weak.
//
// Detaching a subtree demotes its wrappers to weak only; re-attaching
// promotes listener-bearing ones back. When a cleanup reports a node whose
// detached root has no live wrapper left, the subtree is released from the
// node budget.

func (s *Sandbox) wrap(n *dom.Node) goja.Value {
	if n == nil {
		return goja.Null()
	}
	if w := s.liveWrapper(n.ID()); w != nil {
		return w
	}
	w := s.vm.NewObject()
	_ = w.SetPrototype(s.elemProto)
	_ = w.DefineDataPropertySymbol(s.nodeSym, s.vm.ToValue(int(n.ID())), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	s.weakW[n.ID()] = weak.Make(w)
	// The cleanup closure must not reference w (it would never be collected).
	// It references the sandbox, which holds w only weakly.
	runtime.AddCleanup(w, s.collectedCleanup, n.ID())
	return w
}

// collectedCleanup runs on the GC's cleanup goroutine; it only enqueues.
func (s *Sandbox) collectedCleanup(id dom.NodeID) {
	s.collectedMu.Lock()
	s.collected = append(s.collected, id)
	s.collectedMu.Unlock()
}

func (s *Sandbox) liveWrapper(id dom.NodeID) *goja.Object {
	if w, ok := s.strong[id]; ok {
		return w
	}
	if wp, ok := s.weakW[id]; ok {
		if w := wp.Value(); w != nil {
			return w
		}
		delete(s.weakW, id)
	}
	return nil
}

// reap drains cleanup reports (on the VM goroutine) and releases detached
// subtrees that no wrapper keeps alive. Called at the start of every Run and
// after a budget-triggered GC.
func (s *Sandbox) reap() {
	s.collectedMu.Lock()
	ids := s.collected
	s.collected = nil
	s.collectedMu.Unlock()
	for _, id := range ids {
		if wp, ok := s.weakW[id]; ok && wp.Value() == nil {
			delete(s.weakW, id)
		}
		s.forgetCounts(id)
	}
	for _, root := range s.tree.DetachedRoots() {
		if !s.anyLiveWrapper(root) {
			dom.Walk(root, func(n *dom.Node) { s.forgetCounts(n.ID()); delete(s.weakW, n.ID()) })
			s.tree.Release(root)
		}
	}
	if s.active != 0 && s.tree.Node(s.active) == nil {
		s.active = 0
	}
}

func (s *Sandbox) anyLiveWrapper(root *dom.Node) bool {
	alive := false
	dom.Walk(root, func(n *dom.Node) {
		if !alive && s.liveWrapper(n.ID()) != nil {
			alive = true
		}
	})
	return alive
}

// onDetach/onAttach keep the strong map consistent with attachment.
func (s *Sandbox) onDetach(root *dom.Node) {
	dom.Walk(root, func(n *dom.Node) { delete(s.strong, n.ID()) })
	if s.active != 0 && s.tree.Node(s.active) != nil && s.tree.RootOf(s.tree.Node(s.active)) == root {
		s.active = 0 // focus leaves with the subtree, as in browsers
	}
}

func (s *Sandbox) onAttach(root *dom.Node) {
	dom.Walk(root, func(n *dom.Node) {
		if len(s.listenCount[n.ID()]) > 0 {
			if w := s.liveWrapper(n.ID()); w != nil {
				s.strong[n.ID()] = w
			}
		}
	})
}

// node resolves `this` to a live node or throws a TypeError into JS.
func (s *Sandbox) node(this goja.Value) *dom.Node {
	obj, ok := this.(*goja.Object)
	if !ok {
		panic(s.vm.NewTypeError("Illegal invocation"))
	}
	idv := obj.GetSymbol(s.nodeSym)
	if idv == nil {
		panic(s.vm.NewTypeError("Illegal invocation"))
	}
	n := s.tree.Node(dom.NodeID(idv.ToInteger()))
	if n == nil {
		panic(s.vm.NewTypeError("node has been released"))
	}
	return n
}

func (s *Sandbox) nodeArg(v goja.Value) *dom.Node {
	if goja.IsUndefined(v) || goja.IsNull(v) {
		panic(s.vm.NewTypeError("parameter is not a Node"))
	}
	return s.node(v)
}

func (s *Sandbox) throw(err error) {
	if errors.Is(err, dom.ErrLimit) {
		panic(s.vm.NewGoError(err)) // RangeError-like; catchable by the page
	}
	panic(s.vm.NewTypeError(err.Error()))
}

// collectNow is the budget-pressure path: force a GC, give the cleanup
// goroutine a bounded window to report collected wrappers, and reap. It runs
// only when a budget is exceeded, so steady-state pages pay nothing, and the
// window (10 ms) fits inside the default 50 ms slice.
func (s *Sandbox) collectNow() {
	if s.running {
		defer s.wd.pause()() // Go-side GC time is not billed to the script
	}
	runtime.GC()
	deadline := time.Now().Add(10 * time.Millisecond)
	for time.Now().Before(deadline) {
		s.reap()
		time.Sleep(500 * time.Microsecond)
	}
	s.reap()
}

// underPressure retries a budget-limited tree operation once after a forced
// collection, so a page that dropped its references is not punished for the
// GC not having run yet.
func (s *Sandbox) underPressure(op func() error) {
	err := op()
	if errors.Is(err, dom.ErrLimit) {
		s.collectNow()
		err = op()
	}
	s.must(err)
}

func (s *Sandbox) must(err error) {
	if err != nil {
		s.throw(err)
	}
}

func (s *Sandbox) method(obj *goja.Object, name string, fn func(call goja.FunctionCall) goja.Value) {
	defineFrozen(obj, name, s.vm.ToValue(fn))
}

func (s *Sandbox) accessor(obj *goja.Object, name string, get func(goja.Value) goja.Value, set func(goja.Value, goja.Value)) {
	var setter goja.Value
	if set != nil {
		setter = s.vm.ToValue(func(call goja.FunctionCall) goja.Value { set(call.This, call.Argument(0)); return goja.Undefined() })
	}
	getter := s.vm.ToValue(func(call goja.FunctionCall) goja.Value { return get(call.This) })
	_ = obj.DefineAccessorProperty(name, getter, setter, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

func (s *Sandbox) buildElementProto() *goja.Object {
	vm := s.vm
	p := vm.NewObject()
	s.accessor(p, "tagName", func(t goja.Value) goja.Value { return vm.ToValue(strings.ToUpper(s.node(t).Tag())) }, nil)
	s.accessor(p, "nodeType", func(t goja.Value) goja.Value {
		if s.node(t).Kind() == dom.Text {
			return vm.ToValue(3)
		}
		return vm.ToValue(1)
	}, nil)
	s.accessor(p, "isConnected", func(t goja.Value) goja.Value { return vm.ToValue(s.tree.Attached(s.node(t))) }, nil)
	s.accessor(p, "id", func(t goja.Value) goja.Value { v, _ := s.node(t).Attr("id"); return vm.ToValue(v) },
		func(t, v goja.Value) { s.must(s.tree.SetAttr(s.node(t), "id", v.String())) })
	s.accessor(p, "className", func(t goja.Value) goja.Value { v, _ := s.node(t).Attr("class"); return vm.ToValue(v) },
		func(t, v goja.Value) { s.must(s.tree.SetAttr(s.node(t), "class", v.String())) })
	s.accessor(p, "textContent", func(t goja.Value) goja.Value { return vm.ToValue(dom.TextContent(s.node(t))) }, s.setText)
	s.accessor(p, "innerText", func(t goja.Value) goja.Value { return vm.ToValue(dom.TextContent(s.node(t))) }, s.setText)
	// innerHTML is deliberately absent: the setter would require parsing
	// untrusted markup inside the VM slice. Pages build nodes instead.
	s.accessor(p, "parentElement", func(t goja.Value) goja.Value {
		p := s.node(t).Parent()
		if p == nil || p == s.tree.Root() {
			return goja.Null()
		}
		return s.wrap(p)
	}, nil)
	s.accessor(p, "parentNode", func(t goja.Value) goja.Value { return s.wrap(s.node(t).Parent()) }, nil)
	s.accessor(p, "firstChild", func(t goja.Value) goja.Value {
		kids := s.node(t).Children()
		if len(kids) == 0 {
			return goja.Null()
		}
		return s.wrap(kids[0])
	}, nil)
	s.accessor(p, "childNodes", func(t goja.Value) goja.Value { return s.wrapAll(s.node(t).Children(), false) }, nil)
	s.accessor(p, "children", func(t goja.Value) goja.Value { return s.wrapAll(s.node(t).Children(), true) }, nil)
	s.accessor(p, "classList", func(t goja.Value) goja.Value { return s.classList(s.node(t)) }, nil)

	s.method(p, "getAttribute", func(c goja.FunctionCall) goja.Value {
		v, ok := s.node(c.This).Attr(c.Argument(0).String())
		if !ok {
			return goja.Null()
		}
		return vm.ToValue(v)
	})
	s.method(p, "hasAttribute", func(c goja.FunctionCall) goja.Value {
		_, ok := s.node(c.This).Attr(c.Argument(0).String())
		return vm.ToValue(ok)
	})
	s.method(p, "setAttribute", func(c goja.FunctionCall) goja.Value {
		s.must(s.tree.SetAttr(s.node(c.This), c.Argument(0).String(), c.Argument(1).String()))
		return goja.Undefined()
	})
	s.method(p, "removeAttribute", func(c goja.FunctionCall) goja.Value {
		s.tree.RemoveAttr(s.node(c.This), c.Argument(0).String())
		return goja.Undefined()
	})
	s.method(p, "appendChild", func(c goja.FunctionCall) goja.Value {
		child := s.nodeArg(c.Argument(0))
		s.insert(s.node(c.This), child, nil)
		return s.wrap(child)
	})
	s.method(p, "insertBefore", func(c goja.FunctionCall) goja.Value {
		child := s.nodeArg(c.Argument(0))
		var ref *dom.Node
		if r := c.Argument(1); !goja.IsNull(r) && !goja.IsUndefined(r) {
			ref = s.node(r)
		}
		s.insert(s.node(c.This), child, ref)
		return s.wrap(child)
	})
	s.method(p, "removeChild", func(c goja.FunctionCall) goja.Value {
		child := s.nodeArg(c.Argument(0))
		if child.Parent() != s.node(c.This) {
			panic(vm.NewTypeError("node is not a child of this element"))
		}
		s.detach(child)
		return s.wrap(child)
	})
	s.method(p, "remove", func(c goja.FunctionCall) goja.Value { s.detach(s.node(c.This)); return goja.Undefined() })
	s.method(p, "querySelector", func(c goja.FunctionCall) goja.Value { return s.query(s.node(c.This), c.Argument(0).String(), false) })
	s.method(p, "querySelectorAll", func(c goja.FunctionCall) goja.Value { return s.query(s.node(c.This), c.Argument(0).String(), true) })
	s.method(p, "addEventListener", func(c goja.FunctionCall) goja.Value {
		s.addListener(c.This.(*goja.Object), s.node(c.This), c.Argument(0).String(), c.Argument(1))
		return goja.Undefined()
	})
	s.method(p, "removeEventListener", func(c goja.FunctionCall) goja.Value {
		s.removeListener(c.This.(*goja.Object), s.node(c.This), c.Argument(0).String(), c.Argument(1))
		return goja.Undefined()
	})
	s.method(p, "focus", func(c goja.FunctionCall) goja.Value {
		n := s.node(c.This)
		if s.tree.Attached(n) && n.Kind() == dom.Element {
			s.active = n.ID()
		}
		return goja.Undefined()
	})
	s.method(p, "blur", func(c goja.FunctionCall) goja.Value {
		if s.active == s.node(c.This).ID() {
			s.active = 0
		}
		return goja.Undefined()
	})
	return p
}

// insert/detach wrap Tree mutations with the wrapper bookkeeping.
func (s *Sandbox) insert(parent, child, ref *dom.Node) {
	wasAttached := s.tree.Attached(child)
	s.must(s.tree.InsertBefore(parent, child, ref))
	if !wasAttached && s.tree.Attached(child) {
		s.onAttach(child)
	} else if wasAttached && !s.tree.Attached(child) {
		s.onDetach(child)
	}
}

func (s *Sandbox) detach(n *dom.Node) {
	if !s.tree.Attached(n) || n.Parent() == nil {
		return
	}
	s.onDetach(n)
	s.tree.Detach(n)
	if !s.anyLiveWrapper(n) {
		s.tree.Release(n) // nothing can ever reach it again
	}
}

// setText implements the textContent/innerText setter. Replaced children
// become detached roots; those without live wrappers are released at once.
func (s *Sandbox) setText(t, v goja.Value) {
	n := s.node(t)
	prev := append([]*dom.Node(nil), n.Children()...)
	// SetText is transactional in dom: on failure the children stay attached,
	// so their strong pins must only be dropped after it succeeds.
	s.underPressure(func() error { return s.tree.SetText(n, v.String()) })
	for _, c := range prev {
		if !s.tree.Attached(c) {
			s.onDetach(c)
		}
	}
	for _, root := range s.tree.DetachedRoots() {
		if !s.anyLiveWrapper(root) {
			dom.Walk(root, func(n *dom.Node) { s.forgetCounts(n.ID()) })
			s.tree.Release(root)
		}
	}
}

func (s *Sandbox) wrapAll(nodes []*dom.Node, elementsOnly bool) goja.Value {
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		if elementsOnly && n.Kind() != dom.Element {
			continue
		}
		out = append(out, s.wrap(n))
	}
	return s.vm.NewArray(out...)
}

func (s *Sandbox) query(from *dom.Node, sel string, all bool) goja.Value {
	nodes, err := s.tree.Query(from, sel, all)
	if err != nil {
		panic(s.vm.NewGoError(err)) // SyntaxError in browsers; GoError here
	}
	if !all {
		if len(nodes) == 0 {
			return goja.Null()
		}
		return s.wrap(nodes[0])
	}
	return s.wrapAll(nodes, false)
}

func (s *Sandbox) classList(n *dom.Node) goja.Value {
	vm := s.vm
	o := vm.NewObject()
	classes := func() []string { v, _ := n.Attr("class"); return strings.Fields(v) }
	write := func(cs []string) { s.must(s.tree.SetAttr(n, "class", strings.Join(cs, " "))) }
	s.method(o, "contains", func(c goja.FunctionCall) goja.Value { return vm.ToValue(contains(classes(), c.Argument(0).String())) })
	s.method(o, "add", func(c goja.FunctionCall) goja.Value {
		cs := classes()
		for _, a := range c.Arguments {
			if k := a.String(); !contains(cs, k) && k != "" {
				cs = append(cs, k)
			}
		}
		write(cs)
		return goja.Undefined()
	})
	s.method(o, "remove", func(c goja.FunctionCall) goja.Value {
		cs := classes()
		for _, a := range c.Arguments {
			cs = without(cs, a.String())
		}
		write(cs)
		return goja.Undefined()
	})
	s.method(o, "toggle", func(c goja.FunctionCall) goja.Value {
		k := c.Argument(0).String()
		cs := classes()
		if contains(cs, k) {
			write(without(cs, k))
			return vm.ToValue(false)
		}
		write(append(cs, k))
		return vm.ToValue(true)
	})
	return o
}

func contains(cs []string, k string) bool {
	for _, c := range cs {
		if c == k {
			return true
		}
	}
	return false
}

func without(cs []string, k string) []string {
	out := cs[:0]
	for _, c := range cs {
		if c != k {
			out = append(out, c)
		}
	}
	return out
}

// ---- document ----

func (s *Sandbox) documentObject() *goja.Object {
	vm := s.vm
	d := vm.NewObject()
	s.docObj = d
	// The document object participates in event dispatch like a wrapper for
	// the root node, so it gets the same node symbol.
	_ = d.DefineDataPropertySymbol(s.nodeSym, vm.ToValue(int(s.tree.Root().ID())), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	s.strong[s.tree.Root().ID()] = d
	s.method(d, "getElementById", func(c goja.FunctionCall) goja.Value { return s.wrap(s.tree.ByID(c.Argument(0).String())) })
	s.method(d, "querySelector", func(c goja.FunctionCall) goja.Value { return s.query(s.tree.Root(), c.Argument(0).String(), false) })
	s.method(d, "querySelectorAll", func(c goja.FunctionCall) goja.Value { return s.query(s.tree.Root(), c.Argument(0).String(), true) })
	s.method(d, "createElement", func(c goja.FunctionCall) goja.Value {
		var n *dom.Node
		s.underPressure(func() (err error) { n, err = s.tree.NewElement(c.Argument(0).String()); return err })
		return s.wrap(n)
	})
	s.method(d, "createTextNode", func(c goja.FunctionCall) goja.Value {
		var n *dom.Node
		s.underPressure(func() (err error) { n, err = s.tree.NewText(c.Argument(0).String()); return err })
		return s.wrap(n)
	})
	s.method(d, "addEventListener", func(c goja.FunctionCall) goja.Value {
		s.addListener(d, s.tree.Root(), c.Argument(0).String(), c.Argument(1))
		return goja.Undefined()
	})
	s.method(d, "removeEventListener", func(c goja.FunctionCall) goja.Value {
		s.removeListener(d, s.tree.Root(), c.Argument(0).String(), c.Argument(1))
		return goja.Undefined()
	})
	s.accessor(d, "body", func(goja.Value) goja.Value { return s.wrap(s.tree.Body()) }, nil)
	s.accessor(d, "documentElement", func(goja.Value) goja.Value {
		for _, c := range s.tree.Root().Children() {
			if c.Tag() == "html" {
				return s.wrap(c)
			}
		}
		return goja.Null()
	}, nil)
	s.accessor(d, "activeElement", func(goja.Value) goja.Value {
		if n := s.tree.Node(s.active); n != nil && s.tree.Attached(n) {
			return s.wrap(n)
		}
		return s.wrap(s.tree.Body())
	}, nil)
	s.accessor(d, "title", func(goja.Value) goja.Value { return vm.ToValue(s.tree.Title()) }, nil)
	return d
}

// ---- events ----
//
// Listeners live on the wrapper under a hidden symbol: { type: [fn, ...] }.
// Only integer counts live in Go (listenCount), so a dropped wrapper takes
// its callbacks with it and the counts are reconciled by reap.

func (s *Sandbox) listenerList(w *goja.Object, typ string, create bool) *goja.Object {
	bagV := w.GetSymbol(s.listSym)
	bag, _ := bagV.(*goja.Object)
	if bag == nil {
		if !create {
			return nil
		}
		bag = s.vm.NewObject()
		_ = w.DefineDataPropertySymbol(s.listSym, bag, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	}
	arr, _ := bag.Get(typ).(*goja.Object)
	if arr == nil {
		if !create {
			return nil
		}
		arr = s.vm.NewArray()
		_ = bag.Set(typ, arr)
	}
	return arr
}

func arrayValues(arr *goja.Object) []goja.Value {
	if arr == nil {
		return nil
	}
	n := int(arr.Get("length").ToInteger())
	out := make([]goja.Value, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, arr.Get(strconv.Itoa(i)))
	}
	return out
}

func (s *Sandbox) addListener(w *goja.Object, n *dom.Node, typ string, fn goja.Value) {
	if _, ok := goja.AssertFunction(fn); !ok {
		return // browsers ignore non-callable listeners
	}
	if s.nListen >= s.cfg.MaxListeners {
		s.collectNow()
		if s.nListen >= s.cfg.MaxListeners {
			panic(s.vm.NewGoError(errors.New("too many event listeners")))
		}
	}
	arr := s.listenerList(w, typ, true)
	vals := arrayValues(arr)
	for _, existing := range vals {
		if existing.StrictEquals(fn) {
			return
		}
	}
	_ = arr.Set(strconv.Itoa(len(vals)), fn)
	s.nListen++
	if s.listenCount[n.ID()] == nil {
		s.listenCount[n.ID()] = map[string]int{}
	}
	s.listenCount[n.ID()][typ]++
	s.typeCount[typ]++
	s.tree.AddListener(n, typ)
	if s.tree.Attached(n) {
		s.strong[n.ID()] = w
	}
}

func (s *Sandbox) removeListener(w *goja.Object, n *dom.Node, typ string, fn goja.Value) {
	arr := s.listenerList(w, typ, false)
	if arr == nil {
		return
	}
	vals := arrayValues(arr)
	kept := s.vm.NewArray()
	removed := false
	for _, v := range vals {
		if !removed && v.StrictEquals(fn) {
			removed = true
			continue
		}
		_ = kept.Set(strconv.Itoa(int(kept.Get("length").ToInteger())), v)
	}
	if !removed {
		return
	}
	_ = w.GetSymbol(s.listSym).(*goja.Object).Set(typ, kept)
	s.nListen--
	s.typeCount[typ]--
	if m := s.listenCount[n.ID()]; m != nil {
		m[typ]--
		if m[typ] <= 0 {
			delete(m, typ)
			s.tree.RemoveListener(n, typ)
		}
		if len(m) == 0 {
			delete(s.listenCount, n.ID())
			delete(s.strong, n.ID())
		}
	}
}

// forgetCounts reconciles the Go-side counts for a node whose wrapper (and
// with it, its callbacks) is gone or whose subtree is being released.
func (s *Sandbox) forgetCounts(id dom.NodeID) {
	m := s.listenCount[id]
	for typ, c := range m {
		s.nListen -= c
		s.typeCount[typ] -= c
	}
	delete(s.listenCount, id)
	delete(s.strong, id)
}

// Event is what the loop delivers; it is Go data until Dispatch builds the
// JS event object.
type Event struct {
	Type   string
	Target dom.NodeID // 0 targets the focused element (keyboard) or the document
	Key    string     // keydown: DOM KeyboardEvent.key where known
	Href   string     // click: the resolved href of the target or its nearest <a>, if any
}

// Dispatch runs listeners from the target up to the document (bubbling
// only). It returns whether the page called preventDefault. All listener
// calls happen inside one watchdog slice.
func (s *Sandbox) Dispatch(ev Event) (prevented bool, err error) {
	if s.disabled {
		return false, ErrDisabled // nothing can prevent anything any more
	}
	target := s.tree.Root()
	switch {
	case ev.Target != 0:
		if target = s.tree.Node(ev.Target); target == nil || !s.tree.Attached(target) {
			return false, nil
		}
	case ev.Type == "keydown" || ev.Type == "keyup":
		if n := s.tree.Node(s.active); n != nil && s.tree.Attached(n) {
			target = n
		}
	}
	// Collect the path first; listeners may mutate the tree while running.
	type hop struct {
		this *goja.Object
		fns  []goja.Value
	}
	var path []hop
	for n := target; n != nil; n = n.Parent() {
		if w := s.liveWrapper(n.ID()); w != nil {
			if fns := arrayValues(s.listenerList(w, ev.Type, false)); len(fns) > 0 {
				path = append(path, hop{w, fns})
			}
		}
	}
	if len(path) == 0 {
		return false, nil
	}
	err = s.Run(func(vm *goja.Runtime) error {
		stopped := false
		e := vm.NewObject()
		defineFrozen(e, "type", vm.ToValue(ev.Type))
		defineFrozen(e, "target", s.wrap(target))
		defineFrozen(e, "key", vm.ToValue(ev.Key))
		defineFrozen(e, "href", vm.ToValue(ev.Href))
		_ = e.Set("defaultPrevented", false)
		defineFrozen(e, "preventDefault", vm.ToValue(func(goja.FunctionCall) goja.Value {
			prevented = true
			_ = e.Set("defaultPrevented", true)
			return goja.Undefined()
		}))
		defineFrozen(e, "stopPropagation", vm.ToValue(func(goja.FunctionCall) goja.Value { stopped = true; return goja.Undefined() }))
		for _, h := range path {
			if stopped {
				break
			}
			_ = e.Set("currentTarget", h.this)
			for _, f := range h.fns {
				call, _ := goja.AssertFunction(f)
				if _, err := call(h.this, e); err != nil {
					return err // first throwing listener aborts; browsers report and continue
				}
			}
		}
		return nil
	})
	return prevented, err
}

// HasListeners lets the loop skip the VM entirely for events nobody wants.
func (s *Sandbox) HasListeners(typ string) bool { return s.typeCount[typ] > 0 }

// Active reports the focused element (0 = none).
func (s *Sandbox) Active() dom.NodeID { return s.active }

// Focus sets the focused element from the host (e.g. after a click hit a
// focusable element). Non-focusable targets clear focus to the body.
func (s *Sandbox) Focus(id dom.NodeID) {
	n := s.tree.Node(id)
	if n == nil || !s.tree.Attached(n) || !focusable(n) {
		s.active = 0
		return
	}
	s.active = id
}

func focusable(n *dom.Node) bool {
	if _, ok := n.Attr("tabindex"); ok {
		return true
	}
	switch n.Tag() {
	case "a", "button", "input", "select", "textarea", "summary":
		return true
	}
	return false
}
```

## 8. Deliverable 3 — orchestration (`loop/loop.go`, `app/app.go`, `cmd/ntbrowser/main.go`)

Cadence, in order, for one tick of the VM goroutine:

1. Take one item: an inbox task (input, fetch completion, page load), the earliest JS timer, or the frame timer.
2. Run it under one watchdog slice. Promise jobs drain inside the slice.
3. `afterTask`: re-arm the single OS timer to the earliest JS deadline; if the tree is dirty, `scheduleFrame`.
4. `scheduleFrame`: if a render is in flight, mark `pendingFrame` (coalesce); else if less than `MinFrame` (33 ms) since the last frame, arm the frame timer; else `startRender`.
5. `startRender`: `Serialize()` an immutable HTML string, clear dirty, bump `Gen`, hand to a worker goroutine running `document.Render` with nthtml's limits and a 2 s context.
6. Worker posts back: `FrameMsg{Gen, Result}` into the outbox, `requestAnimationFrame` callbacks run (each under its own slice, with a monotonic 10 ms-coarse timestamp since page start), and `afterTask` re-schedules if the tree is dirty or a frame is pending. `requestAnimationFrame` on a clean tree still produces a frame, so rAF-only pages animate.
7. `app.Update` drops any `FrameMsg` with `Gen` ≤ the one on screen. The first frame of a session mounts `nthtml.New(result)` at the current bounds and tells the loop the real document width; every later frame is `viewer.SetResult(result)` followed by `Pan` back to the previous crop offset, unless a `pageLoadedMsg` marked the frame as a new page (decision 17).

Clicks: on a left press+release on the same cell, `app` asks `viewer.LinkAtCell`; a link is left to nthtml, whose `LinkMsg` → `loop.LinkActivated` resolves the DOM anchor, dispatches `click` (bubbling), and emits `NavigateMsg` unless prevented. Any other cell goes through `app.cellToDocument` → `loop.ClickAt(area)`: the loop hit-tests the last frame's `Result.Boxes` (kept per page) for the smallest anchored box, focuses the target, dispatches `click` with bubbling, and emits `NavigateMsg` for an un-prevented click inside an `<a>` ancestor (decision 18).

Input never waits on the VM: `tryPost` drops on a full inbox and counts it. The VM never waits on the UI: `send` goes through a buffered outbox drained by a forwarder goroutine (log lines droppable, frames not). Worker completions use blocking `post`; `LoadPage` from the UI takes a generation, `tryPost`s, and falls back to a goroutine only on a full inbox (decision 23). Timers are one heap plus one reusable `time.Timer`; `setInterval` is clamped to 4 ms; counts are capped. Scripts run in document order: an external `<script src>` suspends the sequence until it arrives, and `DOMContentLoaded` fires once after the last one.

`loop/loop.go`

```go
// Package loop is the orchestrator. One goroutine (the VM goroutine) owns the
// Goja runtime, the dom.Tree, the timer heap and the frame scheduler. Bubble
// Tea's Update goroutine never touches the VM: it posts input through Input
// and LinkActivated and receives FrameMsg / NavigateMsg / LogMsg through the
// Sink. nthtml renders on a worker goroutine from an immutable HTML snapshot.
//
// Neither side ever blocks on the other: the UI posts with tryPost (drop on
// full), and the VM sends through a buffered outbox drained by a forwarder
// goroutine, so a busy Bubble Tea program cannot stall a watchdog slice and a
// slow page cannot stall the terminal.
//
//	[Terminal Key/Mouse] -> tea.Update -> loop.Input ----(inbox)----> VM goroutine
//	                                                                   | dispatch to JS
//	                                                                   | tree dirty?
//	                                                                   v
//	tea.Update <- (outbox) <- FrameMsg(*document.Result) <- render worker <- tree.Serialize()
package loop

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/dop251/goja"
	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/netpolicy"
)

// Sink is satisfied by *tea.Program. It may block; the loop never calls it
// from the VM goroutine directly.
type Sink interface{ Send(tea.Msg) }

// Messages the loop sends to the Bubble Tea model.
type (
	FrameMsg struct {
		Gen    uint64 // monotonically increasing; stale frames are dropped by the host
		Result *document.Result
		Err    error
	}
	// NavigateMsg carries an absolute URL already resolved against the page
	// base. Admission (scheme, host) is the host's decision.
	NavigateMsg struct{ Href string }
	LogMsg      struct{ Level, Line string }
	TitleMsg    struct{ Title string }
)

// Page is one navigation: tree + sandbox + policy. Replacing it discards the
// whole JS heap, which is how cross-page isolation is achieved.
type Page struct {
	Tree    *dom.Tree
	Sandbox *jsvm.Sandbox
	Fetcher *netpolicy.Fetcher
	Base    string
	started time.Time
	loaded  bool
	// ctx bounds every request this page starts; cancel fires when the page
	// is replaced or killed, so no completion outlives its runtime.
	ctx    context.Context
	cancel context.CancelFunc
	killed bool // runtime dropped (heap or strike budget); the last frame stays
	// boxes are the painted boxes of the last frame shown for this page,
	// used to map a document-pixel hit back to an element (Box.Anchor = id).
	boxes []document.Box
}

type Config struct {
	Width       int           // document pixel width handed to nthtml (default 800)
	MinFrame    time.Duration // minimum spacing between re-flows (default 33ms)
	RenderLimit document.Limits
	Fonts       document.FaceSource
	Inbox       int // buffered tasks (default 256)
	Outbox      int // buffered messages to the UI (default 1024)
	Script      jsvm.Config
	// ExternalScripts fetches <script src> through the page's Fetcher. Off
	// by default: reader mode runs inline scripts only.
	ExternalScripts bool
}

type Loop struct {
	cfg    Config
	sink   Sink
	inbox  chan func()
	outbox chan tea.Msg
	ctx    context.Context

	page   *Page
	timers timerHeap
	nextID int
	frames []frameCB

	gen            atomic.Uint64
	navGen         atomic.Uint64 // LoadPage calls, so a stale load can never win
	renderInFlight bool
	pendingFrame   bool
	lastFrame      time.Time
	frameTimer     *time.Timer
	timerT         *time.Timer  // armed to the earliest JS timer deadline
	dropped        atomic.Int64 // input events / log lines dropped under back-pressure
}

func New(ctx context.Context, cfg Config, sink Sink) *Loop {
	if cfg.Width <= 0 {
		cfg.Width = 800
	}
	if cfg.MinFrame <= 0 {
		cfg.MinFrame = 33 * time.Millisecond
	}
	if cfg.Inbox <= 0 {
		cfg.Inbox = 256
	}
	if cfg.Outbox <= 0 {
		cfg.Outbox = 1024
	}
	l := &Loop{ctx: ctx, cfg: cfg, sink: sink, inbox: make(chan func(), cfg.Inbox), outbox: make(chan tea.Msg, cfg.Outbox), timerT: stoppedTimer(), frameTimer: stoppedTimer()}
	go l.forward()
	return l
}

// Run is the VM goroutine. It is the only goroutine that may touch l.page.
func (l *Loop) Run() {
	for {
		select {
		case <-l.ctx.Done():
			return
		case task := <-l.inbox:
			task()
		case <-l.timerT.C:
			l.fireTimers()
		case <-l.frameTimer.C:
			l.startRender()
		}
		l.afterTask()
	}
}

// forward drains the outbox into the (possibly blocking) sink.
func (l *Loop) forward() {
	for {
		select {
		case <-l.ctx.Done():
			return
		case m := <-l.outbox:
			l.sink.Send(m)
		}
	}
}

// send never blocks the VM goroutine. Frames, navigation and titles are
// never dropped (the outbox is sized for them; if it is ever full they
// block, which only happens when the UI goroutine is dead). Log lines are
// droppable.
func (l *Loop) send(m tea.Msg) {
	if _, isLog := m.(LogMsg); isLog {
		select {
		case l.outbox <- m:
		default:
			l.dropped.Add(1)
		}
		return
	}
	select {
	case l.outbox <- m:
	case <-l.ctx.Done():
	}
}

func stoppedTimer() *time.Timer {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return t
}

// armTimer points the single OS timer at the earliest JS deadline.
func (l *Loop) armTimer() {
	l.timerT.Stop()
	if len(l.timers) == 0 {
		return
	}
	l.timerT.Reset(max(0, time.Until(l.timers[0].when)))
}

// post queues work for the VM goroutine and blocks if the inbox is full. Use
// it only from worker goroutines (fetch results, render completions), never
// from the Bubble Tea goroutine.
func (l *Loop) post(fn func()) {
	select {
	case l.inbox <- fn:
	case <-l.ctx.Done():
	}
}

// tryPost drops work when the page cannot keep up. Terminal input uses it so
// a slow script degrades the page, never the terminal.
func (l *Loop) tryPost(fn func()) bool {
	select {
	case l.inbox <- fn:
		return true
	default:
		l.dropped.Add(1)
		return false
	}
}

// ---- page lifecycle ----

// LoadPage parses HTML on the VM goroutine, builds a fresh sandbox, runs the
// page's scripts in document order, fires DOMContentLoaded once every script
// has run, and schedules the first frame. Safe to call from the UI goroutine:
// it hands off without blocking.
func (l *Loop) LoadPage(html string, base string, fetcher *netpolicy.Fetcher) {
	gen := l.navGen.Add(1) // taken synchronously on the caller: call order == generation order
	task := func() {
		if gen != l.navGen.Load() {
			return // a newer LoadPage exists; whichever order the handoffs ran in, it wins
		}
		if l.page != nil {
			l.dropPage(l.page)
		}
		l.timers = nil
		l.frames = nil
		tree, err := dom.Parse(strings.NewReader(html), dom.Limits{})
		if err != nil {
			l.page = nil
			l.send(LogMsg{"error", "parse: " + err.Error()})
			return
		}
		cfg := l.cfg.Script
		cfg.Location = base
		page := &Page{Tree: tree, Fetcher: fetcher, Base: base, started: time.Now()}
		page.ctx, page.cancel = context.WithCancel(l.ctx)
		page.Sandbox = jsvm.New(cfg, tree, (*host)(l))
		l.page = page
		l.send(TitleMsg{tree.Title()})
		l.runScripts(page, 0)
		l.scheduleFrame()
	}
	// Never block the UI goroutine: the inbox normally has room; if it is
	// full, hand off in a goroutine. Ordering is irrelevant because of gen.
	if !l.tryPost(task) {
		go l.post(task)
	}
}

// dropPage discards a page's runtime: scripting is closed so the Goja heap
// is unreachable, in-flight requests are cancelled, timers and frames are
// gone. For a page that tripped the heap or strike budget the tree and the
// last frame stay (links keep working, nothing else does); a replaced page
// is simply forgotten. The GC runs once so the budget is actually reclaimed
// before the next page starts, which is the mitigation §9 promises.
func (l *Loop) dropPage(page *Page) {
	if page.killed {
		return
	}
	page.killed = true
	page.Sandbox.Close()
	if page.cancel != nil {
		page.cancel()
	}
	l.timers = nil
	l.frames = nil
	runtime.GC()
}

// runScripts executes tree.Scripts[i:] in order. An external script suspends
// the sequence until its body arrives, preserving document order; the
// sequence resumes on the VM goroutine.
func (l *Loop) runScripts(page *Page, i int) {
	for ; i < len(page.Tree.Scripts); i++ {
		sc := page.Tree.Scripts[i]
		switch {
		case sc.Inline != "":
			l.report(page.Sandbox.Eval(fmt.Sprintf("%s#inline-%d", page.Base, i), sc.Inline))
		case sc.Src != "" && l.cfg.ExternalScripts:
			next := i + 1
			go func() {
				resp, err := page.Fetcher.Do(page.ctx, netpolicy.Request{Method: "GET", URL: sc.Src})
				l.post(func() {
					if l.page != page {
						return // navigated away
					}
					if err != nil {
						l.report(err)
					} else {
						l.report(page.Sandbox.Eval(sc.Src, resp.Body))
					}
					l.runScripts(page, next)
				})
			}()
			return
		}
	}
	if !page.loaded {
		page.loaded = true
		_, err := page.Sandbox.Dispatch(jsvm.Event{Type: "DOMContentLoaded"})
		l.report(err)
	}
}

func (l *Loop) report(err error) {
	if err != nil {
		l.send(LogMsg{"error", err.Error()})
	}
}

// ---- input routing from the Bubble Tea goroutine ----

// Input forwards a key press as a keydown to the focused element (falling
// back to document) and lets it bubble. It is dropped before reaching the VM
// when the page registered no keydown listener, so an idle page costs
// nothing.
func (l *Loop) Input(key tea.KeyPressMsg) {
	l.tryPost(func() {
		if l.page == nil || !l.page.Sandbox.HasListeners("keydown") {
			return
		}
		_, err := l.page.Sandbox.Dispatch(jsvm.Event{Type: "keydown", Key: domKeyName(key)})
		l.report(err)
	})
}

// domKeyName maps Bubble Tea keystroke names to DOM KeyboardEvent.key values
// for the common keys; printable text passes through unchanged.
func domKeyName(k tea.KeyPressMsg) string {
	if k.Text != "" && k.Mod == 0 {
		return k.Text
	}
	name := k.Keystroke()
	if dom, ok := domKeys[name]; ok {
		return dom
	}
	return name // e.g. "ctrl+s": not a DOM name, but stable and documented
}

var domKeys = map[string]string{
	"enter": "Enter", "esc": "Escape", "escape": "Escape", "tab": "Tab", "space": " ", "backspace": "Backspace", "delete": "Delete",
	"up": "ArrowUp", "down": "ArrowDown", "left": "ArrowLeft", "right": "ArrowRight",
	"home": "Home", "end": "End", "pgup": "PageUp", "pgdown": "PageDown", "insert": "Insert",
}

// LinkActivated runs click listeners on the anchor behind an nthtml LinkMsg.
// If no listener calls preventDefault, a NavigateMsg with the href resolved
// against the page base goes back to the host, which owns navigation.
func (l *Loop) LinkActivated(link document.Link) {
	l.tryPost(func() {
		if l.page == nil {
			return
		}
		href := document.ResolveURL(l.page.Base, link.Href)
		if href == "" {
			l.send(LogMsg{"warn", "link not openable: " + link.Href})
			return
		}
		var prevented bool
		if target := l.anchorFor(link); target != nil {
			var err error
			prevented, err = l.page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: target.ID(), Href: href})
			if !errors.Is(err, jsvm.ErrDisabled) {
				l.report(err) // a dropped runtime cannot prevent; the link just opens
			}
		}
		if !prevented {
			l.send(NavigateMsg{Href: href})
		}
	})
}

// ClickAt dispatches a click to the element under a document-pixel area (the
// host converts the terminal cell with app.cellToDocument; see app/cell.go). The
// target is the smallest painted box carrying an anchor name; anchors are
// element ids, including the synthetic ones the serializer mints for
// listener-bearing elements. Inline elements paint only a 1px marker, so a
// listener on a <span> is reached through its block ancestor. Focus follows
// the click; the nearest <a> supplies the default navigation.
func (l *Loop) ClickAt(area image.Rectangle) {
	l.tryPost(func() {
		if l.page == nil {
			return
		}
		var best *document.Box
		for i := range l.page.boxes {
			b := &l.page.boxes[i]
			if b.Anchor == "" || !b.Bounds.Overlaps(area) {
				continue
			}
			if best == nil || b.Bounds.Dx()*b.Bounds.Dy() < best.Bounds.Dx()*best.Bounds.Dy() {
				best = b
			}
		}
		if best == nil {
			return
		}
		target := l.page.Tree.ByID(best.Anchor)
		if target == nil {
			return
		}
		l.page.Sandbox.Focus(target.ID())
		href := ""
		for n := target; n != nil; n = n.Parent() {
			if n.Tag() == "a" {
				if raw, ok := n.Attr("href"); ok {
					href = document.ResolveURL(l.page.Base, raw)
				}
				break
			}
		}
		prevented, err := l.page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: target.ID(), Href: href})
		if !errors.Is(err, jsvm.ErrDisabled) {
			l.report(err)
		}
		if !prevented && href != "" {
			l.send(NavigateMsg{Href: href})
		}
	})
}

// anchorFor maps an nthtml Link back to the DOM anchor. nthtml identifies
// links by a stable ID in document order over *visible, safe* anchors; the
// tree cannot compute visibility, so match on href+text and take the first
// candidate. (A Link.Anchor field in nthtml would make this exact; §11.)
func (l *Loop) anchorFor(link document.Link) *dom.Node {
	// nthtml reports Href as document.SafeURL(attr) and Text as the cleaned,
	// whitespace-collapsed label (alt text included), or the href itself when
	// the label is empty (<a href><img></a>). Compare in its terms, href
	// first; text only disambiguates duplicates. A Link.Anchor field in
	// nthtml would make this exact (design §11).
	anchors, _ := l.page.Tree.Query(l.page.Tree.Root(), "a", true)
	var byHref []*dom.Node
	for _, a := range anchors {
		if raw, ok := a.Attr("href"); ok && document.SafeURL(raw) == link.Href {
			byHref = append(byHref, a)
		}
	}
	switch len(byHref) {
	case 0:
		return nil
	case 1:
		return byHref[0]
	}
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	want := norm(link.Text)
	for _, a := range byHref {
		if txt := norm(dom.TextContent(a)); txt == want || (txt == "" && link.Text == link.Href) {
			return a
		}
	}
	return byHref[0]
}

// Resize changes the layout width and forces a re-flow of the current tree.
func (l *Loop) Resize(widthPx int) {
	l.tryPost(func() {
		if widthPx > 0 && widthPx != l.cfg.Width {
			l.cfg.Width = widthPx
			l.pendingFrame = true
			l.scheduleFrame()
		}
	})
}

// Dropped reports how many inputs/log lines were shed under back-pressure.
func (l *Loop) Dropped() int64 { return l.dropped.Load() }

// ---- frame scheduler ----

func (l *Loop) afterTask() {
	l.armTimer()
	if l.page == nil {
		return
	}
	if l.page.Sandbox.Disabled() && !l.page.killed {
		l.dropPage(l.page) // heap or strike budget: free the runtime, keep the last frame
	}
	if l.page.Tree.Dirty() || l.pendingFrame {
		l.scheduleFrame()
	}
}

func (l *Loop) scheduleFrame() {
	if l.page == nil {
		return
	}
	if l.renderInFlight {
		l.pendingFrame = true // coalesce: one render at a time, latest tree wins
		return
	}
	if wait := l.cfg.MinFrame - time.Since(l.lastFrame); wait > 0 {
		l.frameTimer.Reset(wait)
		return
	}
	l.startRender()
}

func (l *Loop) startRender() {
	if l.page == nil || l.renderInFlight {
		return
	}
	page := l.page
	html := page.Tree.Serialize() // immutable snapshot; safe to hand off
	page.Tree.ClearDirty()
	l.pendingFrame = false
	l.renderInFlight = true
	l.lastFrame = time.Now()
	gen := l.gen.Add(1)
	opts := document.Options{Width: l.cfg.Width, Limits: l.cfg.RenderLimit, Fonts: l.cfg.Fonts}
	go func() {
		ctx, cancel := context.WithTimeout(l.ctx, 2*time.Second)
		defer cancel()
		// Render enforces nthtml's own byte/node/pixel budgets; a hostile tree
		// fails here with document.ErrLimit instead of exhausting memory.
		res, err := document.Render(ctx, strings.NewReader(html), opts)
		l.post(func() {
			l.renderInFlight = false
			if l.page != page {
				return // frame for a page we navigated away from
			}
			if res != nil {
				page.boxes = res.Boxes
			}
			l.send(FrameMsg{Gen: gen, Result: res, Err: err})
			l.runFrameCallbacks()
		})
	}()
}

type frameCB struct {
	id int
	fn goja.Callable
}

// runFrameCallbacks delivers requestAnimationFrame callbacks after a frame
// was produced, each under its own slice, with a monotonic millisecond
// timestamp since page start (DOMHighResTimeStamp semantics, 10 ms coarse).
func (l *Loop) runFrameCallbacks() {
	if len(l.frames) == 0 || l.page == nil {
		return
	}
	cbs := l.frames
	l.frames = nil
	sb := l.page.Sandbox
	ts := float64(time.Since(l.page.started).Truncate(10*time.Millisecond)) / float64(time.Millisecond)
	for _, cb := range cbs {
		l.report(sb.Call(cb.fn, goja.Undefined(), sb.ToValue(ts)))
	}
}

// ---- timers ----

type timer struct {
	id       int
	when     time.Time
	interval time.Duration
	repeat   bool
	fn       goja.Callable
	args     []goja.Value
	index    int
}

type timerHeap []*timer

func (h timerHeap) Len() int           { return len(h) }
func (h timerHeap) Less(i, j int) bool { return h[i].when.Before(h[j].when) }
func (h timerHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index, h[j].index = i, j }
func (h *timerHeap) Push(x any)        { t := x.(*timer); t.index = len(*h); *h = append(*h, t) }
func (h *timerHeap) Pop() any          { old := *h; t := old[len(old)-1]; *h = old[:len(old)-1]; return t }
func (h timerHeap) find(id int) *timer {
	for _, t := range h {
		if t.id == id {
			return t
		}
	}
	return nil
}

// fireTimers runs every due timer, each in its own watchdog slice, so one
// slow callback cannot starve the others or the frame.
func (l *Loop) fireTimers() {
	now := time.Now()
	for len(l.timers) > 0 && !l.timers[0].when.After(now) {
		t := heap.Pop(&l.timers).(*timer)
		if t.repeat {
			t.when = now.Add(t.interval)
			heap.Push(&l.timers, t)
		}
		if l.page == nil {
			continue
		}
		err := l.page.Sandbox.Call(t.fn, goja.Undefined(), t.args...)
		if errors.Is(err, jsvm.ErrDisabled) {
			l.timers = nil
			return
		}
		l.report(err)
	}
}

// ---- jsvm.Host implementation (called on the VM goroutine) ----

type host Loop

func (h *host) SetTimer(delay time.Duration, repeat bool, fn goja.Callable, args []goja.Value) (int, bool) {
	l := (*Loop)(h)
	limit := l.cfg.Script.MaxTimers
	if limit <= 0 {
		limit = 256
	}
	if len(l.timers) >= limit {
		return 0, false
	}
	l.nextID++
	heap.Push(&l.timers, &timer{id: l.nextID, when: time.Now().Add(delay), interval: delay, repeat: repeat, fn: fn, args: args})
	return l.nextID, true
}

func (h *host) ClearTimer(id int) {
	l := (*Loop)(h)
	if t := l.timers.find(id); t != nil {
		heap.Remove(&l.timers, t.index)
	}
}

// RequestFrame schedules a frame even when the tree is clean, so a page that
// only animates through rAF (reading state, drawing later) still gets its
// callback at the frame cadence.
func (h *host) RequestFrame(fn goja.Callable) int {
	l := (*Loop)(h)
	if len(l.frames) >= 256 {
		return 0
	}
	l.nextID++
	l.frames = append(l.frames, frameCB{id: l.nextID, fn: fn})
	l.pendingFrame = true
	return l.nextID
}

func (h *host) CancelFrame(id int) {
	l := (*Loop)(h)
	for i, f := range l.frames {
		if f.id == id {
			l.frames = append(l.frames[:i], l.frames[i+1:]...)
			return
		}
	}
}

// Fetch runs the request on a worker and posts the completion back to the VM
// goroutine, which is the only place the Promise may be settled.
func (h *host) Fetch(req jsvm.FetchRequest, done func(jsvm.FetchResponse, error)) error {
	l := (*Loop)(h)
	page := l.page
	if page == nil {
		return errors.New("no page")
	}
	go func() {
		resp, err := page.Fetcher.Do(page.ctx, netpolicy.Request{Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body})
		l.post(func() {
			if l.page != page {
				return // promise belongs to a discarded runtime; let it be collected
			}
			done(jsvm.FetchResponse{Status: resp.Status, StatusText: resp.StatusText, URL: resp.URL, Headers: resp.Headers, Body: resp.Body}, err)
		})
	}()
	return nil
}

func (h *host) Log(level, line string) { (*Loop)(h).send(LogMsg{Level: level, Line: line}) }
```

`app/app.go`

```go
// Package app is the Bubble Tea host. It owns the terminal, the nthtml viewer,
// navigation policy and history. It knows nothing about JavaScript beyond the
// loop's messages.
package app

import (
	"context"
	"fmt"
	"image"
	"io"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
)

// NavMode is how link navigation is admitted (decision 11, user-configurable).
type NavMode int

const (
	// NavFree: a user-activated link may open any http(s) origin, like a browser.
	NavFree NavMode = iota
	// NavAllowlist: navigation is confined to the current origin plus -allow.
	NavAllowlist
	// NavConfirm: leaving the current origin asks in the status line first.
	NavConfirm
)

func ParseNavMode(s string) (NavMode, error) {
	switch strings.ToLower(s) {
	case "", "free":
		return NavFree, nil
	case "allowlist":
		return NavAllowlist, nil
	case "confirm":
		return NavConfirm, nil
	}
	return 0, fmt.Errorf("unknown navigation mode %q (free|allowlist|confirm)", s)
}

type pageLoadedMsg struct {
	html string
	base *url.URL
	seq  uint64 // navigation sequence; only the latest navigation may land
	hist int    // history index to make current, or pushNew
	err  error
}

// pushNew means "truncate forward history and append" (a link, the start
// page, a confirmed prompt); any other value is Back/Forward/Reload.
const pushNew = -1

type shutdownReadyMsg struct{}

const shutdownGrace = 150 * time.Millisecond

type Model struct {
	loop    *loop.Loop
	viewer  *nthtml.Model
	policy  netpolicy.Policy // template; Origin is set per navigation
	nav     NavMode
	cols    int
	rows    int
	gen     uint64
	title   string
	status  string
	closing bool
	start   string

	current    *url.URL
	history    []string
	hIndex     int // index of current in history; -1 before the first load
	pendingNav string

	// resetView marks the next frame as the first of a new page: scroll is
	// not carried over. pressCell/pressed turn a press+release on the same
	// cell into an element click (browser semantics, and the same rule nthtml
	// uses before it emits LinkMsg).
	resetView bool
	pressCell image.Point
	pressed   bool
	// navSeq counts navigations started; a pageLoadedMsg carrying an older
	// sequence is a slower, superseded load and is dropped. History is only
	// touched when a load succeeds (decision 20).
	navSeq uint64
}

func New(l *loop.Loop, policy netpolicy.Policy, nav NavMode, start string) *Model {
	return &Model{loop: l, policy: policy, nav: nav, cols: 80, rows: 24, start: start, hIndex: -1}
}

func (m *Model) Init() tea.Cmd { return m.navigate(m.start, pushNew, true) }

// navigate loads an absolute URL and hands the HTML to the loop.
//
// Top-level navigation and a page's fetch() are two policies (decision 11):
// navigation under -nav free, or after a confirm, may go to any http(s) host
// and follow redirects across scheme and host (anyOrigin); under -nav
// allowlist, and for a same-origin link in confirm mode, redirects must stay
// inside the current origin plus -allow. Both still refuse credentials,
// local names, forbidden literal and resolved addresses, cookies and bodies
// over 1 MiB, because they share netpolicy's dialer.
//
// file: is accepted only for the start page the host asked for; links can
// never produce it because document.ResolveURL rejects local schemes. The
// page cannot navigate on its own (no location.assign); every navigation is
// a user action, filtered by the NavMode in Update.
func (m *Model) navigate(href string, hist int, anyOrigin bool) tea.Cmd {
	m.navSeq++
	seq := m.navSeq
	firstPage := m.hIndex < 0 // captured on the Update goroutine; the Cmd runs elsewhere
	policy := m.policy
	policy.Origin, policy.AnyOrigin, policy.MaxBody = m.current, anyOrigin, 1<<20
	return func() tea.Msg {
		u, err := url.Parse(href)
		if err != nil {
			return pageLoadedMsg{seq: seq, err: err}
		}
		switch u.Scheme {
		case "file":
			if !firstPage {
				return pageLoadedMsg{seq: seq, err: fmt.Errorf("file: navigation is only allowed for the start page")}
			}
			data, err := readLocal(u.Path)
			return pageLoadedMsg{html: data, base: u, seq: seq, hist: hist, err: err}
		case "http", "https":
		default:
			return pageLoadedMsg{seq: seq, err: fmt.Errorf("refusing to open %s", u.Scheme)}
		}
		if policy.Origin == nil {
			policy.Origin = u // first page: nothing to be same-origin with but itself
		}
		f := netpolicy.New(policy)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resp, err := f.Do(ctx, netpolicy.Request{Method: "GET", URL: u.String()})
		if err != nil {
			return pageLoadedMsg{seq: seq, err: err}
		}
		final, _ := url.Parse(resp.URL)
		return pageLoadedMsg{html: resp.Body, base: final, seq: seq, hist: hist}
	}
}

// anyOriginFor is the navigation policy for a URL the user already admitted
// (Back, Forward, Reload): only -nav allowlist keeps redirects confined.
func (m *Model) anyOriginFor() bool { return m.nav != NavAllowlist }

// admit applies the NavMode to a link the page did not prevent.
func (m *Model) admit(href string) tea.Cmd {
	u, err := url.Parse(href)
	if err != nil {
		m.status = clean("bad link: " + err.Error())
		return nil
	}
	sameOrigin := m.current != nil && m.current.Scheme == u.Scheme && strings.EqualFold(m.current.Host, u.Host)
	switch m.nav {
	case NavAllowlist:
		p := m.policy
		p.Origin = m.current
		if err := netpolicy.New(p).Allowed(u); err != nil {
			m.status = clean("blocked: " + err.Error())
			return nil
		}
	case NavConfirm:
		if !sameOrigin {
			m.pendingNav = href
			m.status = clean("Open " + href + "? Enter to open · Esc to stay")
			return nil
		}
		return m.navigate(href, pushNew, false) // same-origin: a redirect off-origin is refused, not silently followed
	}
	return m.navigate(href, pushNew, m.nav == NavFree)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.closing {
		if _, ok := msg.(shutdownReadyMsg); ok {
			return m, tea.Quit
		}
		if m.viewer != nil {
			return m, m.viewer.Update(msg) // graphics cleanup still flows
		}
		return m, nil
	}
	switch v := msg.(type) {
	case pageLoadedMsg:
		if v.seq != m.navSeq {
			return m, nil // superseded by a later navigation
		}
		if v.err != nil {
			m.status = clean("load failed: " + v.err.Error()) // errors echo page-controlled input
			return m, nil
		}
		m.current = v.base
		m.resetView = true
		// History moves only now, on success, so Back/Forward always name
		// the page on screen.
		if v.hist == pushNew {
			m.history = append(m.history[:m.hIndex+1], v.base.String())
			m.hIndex = len(m.history) - 1
		} else if v.hist >= 0 && v.hist < len(m.history) {
			m.hIndex = v.hist
			m.history[v.hist] = v.base.String() // a redirect may have moved it
		}
		p := m.policy
		p.Origin = v.base
		m.loop.LoadPage(v.html, v.base.String(), netpolicy.New(p))
		m.status = clean("loading " + v.base.String())
		return m, nil

	case loop.FrameMsg:
		// Frames arrive from the render worker; drop anything older than
		// what is on screen so a slow render cannot regress the view.
		if v.Gen <= m.gen {
			return m, nil
		}
		m.gen = v.Gen
		if v.Err != nil {
			m.status = clean("render: " + v.Err.Error())
			return m, nil
		}
		m.status = clean(strings.Join(v.Result.Warnings, "; "))
		if m.viewer == nil {
			// nthtml.New leaves ReflowTypeset off, which is what the host
			// wants: the loop re-flows on Resize, at its own width, off the
			// UI goroutine; the viewer must never re-render on its own.
			m.viewer = nthtml.New(v.Result)
			m.resetView = false
			cmd := tea.Batch(m.viewer.Init(), m.viewer.SetBounds(0, 0, m.cols, m.viewRows()))
			m.loop.Resize(documentWidth(m.viewer))
			return m, cmd
		}
		// Re-flow in place (nthtml v0.1.0 Model.SetResult). SetResult resets
		// scroll, zoom, search and the focused link; the host puts the scroll
		// offset back from the previous crop so a page that re-flows
		// continuously stays where the reader left it. Pan clamps to the new
		// page, so a shorter page simply ends at its bottom. Zoom is not
		// recoverable through the public API (design §11).
		crop := m.viewer.Picture().Crop()
		cmd := m.viewer.SetResult(v.Result)
		m.loop.Resize(documentWidth(m.viewer)) // no-op unless the terminal's pixel geometry changed
		if m.resetView || crop.Empty() {
			m.resetView = false
			return m, cmd
		}
		return m, tea.Batch(cmd, m.viewer.Pan(crop.Min.X, crop.Min.Y))

	case loop.NavigateMsg:
		return m, m.admit(v.Href)
	case loop.TitleMsg:
		m.title = clean(v.Title) // page text: never trusted at the terminal boundary
	case loop.LogMsg:
		m.status = clean(fmt.Sprintf("[%s] %s", v.Level, v.Line)) // jsvm strips console text; error strings may still echo page input

	case nthtml.LinkMsg:
		if v.Owner != m.viewer {
			return m, nil
		}
		// Keyboard (Tab/Enter) and mouse activation of links both arrive
		// here; the page's click listeners decide the default action first.
		m.loop.LinkActivated(v.Link)
		return m, nil
	case nthtml.CopyMsg:
		return m, tea.SetClipboard(v.Text)

	case tea.MouseClickMsg:
		m.pressed = v.Button == tea.MouseLeft
		m.pressCell = image.Pt(v.X, v.Y)
	case tea.MouseReleaseMsg:
		p := image.Pt(v.X, v.Y)
		click := m.pressed && p == m.pressCell
		m.pressed = false
		if click && m.viewer != nil {
			// A link under the cell is nthtml's: it emits LinkMsg for this
			// same release and the loop handles it once. Anything else is an
			// element click the viewer knows nothing about.
			if _, isLink := m.viewer.LinkAtCell(p); !isLink {
				if area, ok := cellToDocument(m.viewer, p); ok {
					m.loop.ClickAt(area)
				}
			}
		}

	case tea.KeyPressMsg:
		searching := m.viewer != nil && m.viewer.SearchInputActive()
		if m.pendingNav != "" && !searching {
			switch v.String() {
			case "enter":
				href := m.pendingNav
				m.pendingNav = ""
				return m, m.navigate(href, pushNew, true) // the reader confirmed: any origin
			case "esc":
				m.pendingNav = ""
				m.status = clean("stayed on " + m.current.String())
				return m, nil
			}
		}
		if !searching {
			switch v.String() {
			case "q", "ctrl+c":
				m.closing = true
				var closeCmd tea.Cmd
				if m.viewer != nil {
					closeCmd = m.viewer.Close()
				}
				return m, tea.Sequence(closeCmd, tea.Tick(shutdownGrace, func(time.Time) tea.Msg { return shutdownReadyMsg{} }))
			case "backspace", "alt+left":
				return m, m.step(-1)
			case "alt+right":
				return m, m.step(+1)
			case "ctrl+r":
				if m.current != nil {
					return m, m.navigate(m.current.String(), m.hIndex, m.anyOriginFor())
				}
				return m, nil
			}
			// The terminal keeps its own navigation keys; the page gets a copy
			// as keydown and cannot swallow them. Text typed into the viewer's
			// / search box never reaches the page.
			m.loop.Input(v)
		}

	case tea.WindowSizeMsg:
		m.cols, m.rows = v.Width, v.Height
		if m.viewer != nil {
			cmd := tea.Batch(m.viewer.SetBounds(0, 0, v.Width, m.viewRows()), m.viewer.Update(msg))
			m.loop.Resize(documentWidth(m.viewer)) // re-flow at the terminal's real pixel width
			return m, cmd
		}
		return m, nil
	}
	if m.viewer != nil {
		return m, m.viewer.Update(msg)
	}
	return m, nil
}

// viewRows is the viewer's height: the terminal minus the two status lines.
func (m *Model) viewRows() int { return max(1, m.rows-2) }

// step moves through history. Pages are always re-fetched (no cache) so a
// Back never shows state a script produced earlier.
func (m *Model) step(delta int) tea.Cmd {
	i := m.hIndex + delta
	if i < 0 || i >= len(m.history) {
		return nil
	}
	return m.navigate(m.history[i], i, m.anyOriginFor()) // hIndex moves when the load succeeds
}

func (m *Model) View() tea.View {
	body := ""
	if m.viewer != nil {
		body = m.viewer.View()
	}
	// Everything on these two lines passed clean() when stored; truncation
	// only measures width. No page text reaches here unfiltered.
	status := ansi.Truncate(clean(m.status), max(1, m.cols), "…")
	pos := ""
	if len(m.history) > 1 {
		pos = fmt.Sprintf(" · %d/%d", m.hIndex+1, len(m.history))
	}
	v := tea.NewView(body + "\n" + status + "\n ntbrowser · " + ansi.Truncate(clean(m.title), 40, "…") + pos + " · Tab links · Enter open · ⌫ back · ⌥→ fwd · ^R reload · / search · q quit")
	v.AltScreen = true
	if !m.closing {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// readLocal is the host's own file access, bounded like nthtml's parser.
// It lives in the app, never in jsvm.
var readLocal = func(path string) (string, error) {
	return "", fmt.Errorf("local files disabled in this build: %s", path)
}

// SetLocalReader lets cmd/ntbrowser install an os-backed reader.
func SetLocalReader(open func(path string) (io.ReadCloser, error)) {
	readLocal = func(path string) (string, error) {
		r, err := open(path)
		if err != nil {
			return "", err
		}
		defer r.Close()
		b, err := io.ReadAll(io.LimitReader(r, int64(document.DefaultLimits.Bytes)+1))
		if err != nil {
			return "", err
		}
		if len(b) > document.DefaultLimits.Bytes {
			return "", document.ErrLimit
		}
		return string(b), nil
	}
}
```

`app/cell.go` is the host copy of nthtml's cell→document mapping (decision 13); `app/clean.go` is the terminal boundary (decision 7):

`app/cell.go`

```go
package app

import (
	"image"
	"math"

	"nimbleterminal.dev/nthtml"
)

// cellToDocument maps a terminal cell to the document-pixel area it shows,
// using only what nthtml v0.1.0 exports: the widget bounds, the picture's
// raster geometry and the current source crop. It is the same arithmetic as
// nthtml's unexported rasterCellSource (model_mouse.go) and cell_test.go
// checks it against Model.LinkAtCell, which uses that path. It reports false
// in TTY layout mode, where cells index TTY lines rather than document
// pixels, and before the viewer has a Result.
//
// If nthtml later exports this (design §11), delete this file and call it.
func cellToDocument(m *nthtml.Model, p image.Point) (image.Rectangle, bool) {
	if m == nil || m.LayoutMode() != nthtml.LayoutTypeset || m.Result() == nil {
		return image.Rectangle{}, false
	}
	b := m.Bounds()
	p = p.Sub(b.Min)
	cells := b.Size()
	if p.X < 0 || p.Y < 0 || p.X >= cells.X || p.Y >= cells.Y {
		return image.Rectangle{}, false
	}
	pixels := m.Picture().RasterGeometry().Pixels
	crop := m.Picture().Crop()
	if crop.Empty() {
		crop = m.Result().Bounds()
	}
	if cells.X <= 0 || cells.Y <= 0 || pixels.X <= 0 || pixels.Y <= 0 || crop.Empty() {
		return image.Rectangle{}, false
	}
	// Letterbox the crop into the raster target, then project the cell back.
	tw, th := pixels.X, pixels.Y
	sw, sh := crop.Dx(), crop.Dy()
	iw, ih := tw, th
	if int64(sw)*int64(th) >= int64(sh)*int64(tw) {
		ih = max(1, int(int64(sh)*int64(tw)/int64(sw)))
	} else {
		iw = max(1, int(int64(sw)*int64(th)/int64(sh)))
	}
	ox, oy := (tw-iw)/2, (th-ih)/2
	left := math.Max(float64(ox), float64(p.X)*float64(tw)/float64(cells.X))
	top := math.Max(float64(oy), float64(p.Y)*float64(th)/float64(cells.Y))
	right := math.Min(float64(ox+iw), float64(p.X+1)*float64(tw)/float64(cells.X))
	bottom := math.Min(float64(oy+ih), float64(p.Y+1)*float64(th)/float64(cells.Y))
	if right <= left || bottom <= top {
		return image.Rectangle{}, false
	}
	area := image.Rect(
		crop.Min.X+int(math.Floor((left-float64(ox))*float64(sw)/float64(iw))),
		crop.Min.Y+int(math.Floor((top-float64(oy))*float64(sh)/float64(ih))),
		crop.Min.X+int(math.Ceil((right-float64(ox))*float64(sw)/float64(iw))),
		crop.Min.Y+int(math.Ceil((bottom-float64(oy))*float64(sh)/float64(ih))),
	).Intersect(crop)
	return area, !area.Empty()
}

// documentWidth is the CSS-pixel width the loop should lay pages out at for
// this viewer. It mirrors nthtml's own ReflowTypeset choice (model.go
// SetSize): the raster target width, or ten pixels per column when the
// picture is in glyph fallback (one pixel per cell) or has no geometry yet,
// clamped to [32, 4096].
func documentWidth(m *nthtml.Model) int {
	cols := m.Bounds().Dx()
	w := m.Picture().RasterGeometry().Pixels.X
	if w <= 0 || w == cols {
		w = cols * 10
	}
	return max(32, min(4096, w))
}
```

`app/clean.go`

```go
package app

import "strings"

// clean is the terminal boundary for page-controlled text: titles, link
// hrefs in prompts, render warnings, error strings that echo page input, and
// log lines (which jsvm already strips; this is the second layer). It drops
// C0 controls, DEL, C1 controls and the Unicode line/paragraph separators,
// so nothing the status line prints can start an escape sequence or break a
// line. It is not ansi.Truncate, which preserves sequences.
func clean(s string) string {
	if strings.IndexFunc(s, unsafeRune) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unsafeRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unsafeRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x2028 || r == 0x2029
}
```

`cmd/ntbrowser/main.go`

```go
// Command ntbrowser runs a sandboxed page in the terminal.
//
//	ntbrowser https://example.com/page.html
//	ntbrowser -allow .example.com -nav confirm -width 800 file:///path/to/page.html
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/app"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
)

func main() {
	allow := flag.String("allow", "", "comma-separated extra hosts fetch() may reach (leading dot = subdomains)")
	private := flag.Bool("allow-private", false, "permit loopback/private addresses (development)")
	width := flag.Int("width", 800, "document pixel width")
	slice := flag.Duration("slice", 50*time.Millisecond, "JS time slice per callback")
	external := flag.Bool("external-scripts", false, "fetch <script src> through the allowlist")
	navFlag := flag.String("nav", "free", "link navigation: free (any http(s) origin), allowlist (origin + -allow), confirm (ask before leaving the origin)")
	flag.Parse()
	nav, err := app.ParseNavMode(*navFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: ntbrowser [flags] <url|file://path>")
		os.Exit(2)
	}
	// Process-wide soft heap cap: Goja has no per-runtime memory limit, so
	// the GC is told to work harder past this point and dom/nthtml limits
	// keep individual allocations bounded.
	debug.SetMemoryLimit(512 << 20)

	policy := netpolicy.Policy{AllowPrivate: *private}
	if *allow != "" {
		policy.AllowHosts = strings.Split(*allow, ",")
	}
	// The only os.Open in the program; installed into app, never jsvm.
	app.SetLocalReader(func(path string) (io.ReadCloser, error) { return os.Open(path) })

	var program *tea.Program
	sink := sinkFunc(func(msg tea.Msg) { program.Send(msg) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Width: *width, Script: jsvm.Config{Slice: *slice}, ExternalScripts: *external}, sink)
	go l.Run()

	m := app.New(l, policy, nav, flag.Arg(0))
	program = tea.NewProgram(m)
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type sinkFunc func(tea.Msg)

func (f sinkFunc) Send(msg tea.Msg) { f(msg) }
```

## 9. Threat model → control → where

| Threat | Control | Enforced in |
| --- | --- | --- |
| Script reads/writes files | no `os` import; source maps disabled at the `Parse` call (not just on the runtime); `file:` rejected by `Allowed` and `ResolveURL` | `jsvm` (three tests), `netpolicy` |
| Script opens sockets / SSRF to metadata or LAN | only `fetch` exists; scheme/host allowlist; DNS-resolved IP vetted then dialed (`netip`-based: loopback, RFC1918, link-local, CGNAT, multicast, plus NAT64 `64:ff9b::/96` by embedded address, `64:ff9b:1::/48`, 6to4 by embedded address, Teredo, IPv4-compatible); redirects re-admitted; no proxy env; top-level navigation uses the same dialer with `AnyOrigin` | `netpolicy` (`TestForbiddenIPCoversTransitionPrefixes`) |
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
| Frame storm | dirty flag + coalescing + `MinFrame`; one render in flight; stale frames dropped by `Gen` | `loop`, `app` |

## 10. Verification performed on the spike

```
GOWORK=off CGO_ENABLED=0 go build ./...      # ok
GOWORK=off CGO_ENABLED=0 go vet ./...        # ok
GOWORK=off go test -race -count=3 ./...      # ok: app, dom, jsvm, netpolicy, loop
```

Tests included (and what they prove):

- `jsvm.TestNoSystemImports` — `jsvm`/`dom` import none of `os`, `os/exec`, `net`, `net/http`, `syscall`, `io/fs`, `plugin`, `unsafe`.
- `jsvm.TestGlobalSurface` — no `eval`/`require`/`process`/`XMLHttpRequest`/`WebSocket`/`Worker`/`WebAssembly`; host globals present.
- `jsvm.TestWatchdogAndStrikes` — `for(;;){}` is interrupted; second strike disables scripting; later `Eval` returns `ErrDisabled`.
- `jsvm.TestBridgeMutatesTreeNotPointers` — `textContent` with markup is escaped on serialization; a listener on an id-less element mints `nt-h-N`; `preventDefault` is observed by `Dispatch`.
- `jsvm.TestConsoleStripsTerminalEscapes` — an OSC 52 clipboard sequence in `console.log` never reaches the host.
- `jsvm.TestOnlyHardenedCompilePath` — static scan: no `goja.Compile`/`MustCompile`/`RunString`/`RunScript` in `jsvm`, and every `goja.Parse` passes options.
- `jsvm.TestSourceMapCommentNeverTouchesFilesystem` — positive control first: `goja.Compile` on a script whose `sourceMappingURL` is a `file://` URL to a temp map remaps the thrown error's `Position().Filename` to `SECRET_SOURCE.js`; the sandbox's compile path on the same script does not. Getting the control to fire took three attempts (a missing file is silent, `SrcName()` is never remapped, bare paths resolve relative to the script name, and go-sourcemap matches columns exactly), which is why the control is part of the test: without it the test passes vacuously.
- `jsvm.TestTextContentSetterReleasesListeners` — ten rounds of `textContent = ""` + three listeners each stay within a 4-listener budget (exercises the forced-collection path).
- `jsvm.TestDetachKeepsNodeAndListenersAlive` — `remove()` then `appendChild()` re-inserts the same node with its click listener intact.
- `jsvm.TestDetachedSubtreesAreReleasedAfterGC` — 200 detached nodes with listeners are released from a 400-node budget after GC, listener counts reconcile to zero, and the budget is usable again.
- `jsvm.TestAttachedListenerSurvivesWrapperDrop` — a listener on an attached element fires after the page dropped every reference and the GC ran.
- `jsvm.TestFocusRoutesKeydown` — `focus()` moves `activeElement`; `keydown` hits the button then bubbles to `document`; a non-focusable click target clears focus.
- `app.TestCellToDocumentAgreesWithLinkAtCell` — a real render in a headless `nthtml.Model` at a non-zero origin: for every cell in a 90×30 grid, nthtml's `LinkAtCell` sees a link exactly when the host's `cellToDocument` area overlaps that link's regions; repeated after `Pan(0, 700)`; `documentWidth` gives 800 for 80 glyph-fallback columns.
- `app.TestReflowKeepsScrollAndNewPageResetsIt` — `FrameMsg` 1 mounts the viewer, `Pan(0, 900)`, `FrameMsg` 2 (new `Result`) keeps crop y = 900 and shows the new document, a stale `Gen` 1 frame is ignored, `pageLoadedMsg` + `FrameMsg` 3 starts at y = 0 and pushes history.
- `loop.TestClickAtHitsElementsAndMintedAnchors` — a real render; clicking inside an id'd card's paragraph dispatches to the card; clicking a listener-bearing `div` without an id reaches it through the minted `nt-h-N` anchor.
- `dom.TestCreatedNodesAreDetachedUntilInserted`, `dom.TestInsertBeforeSelfIsAMoveToTheSamePlace`, `dom.TestSetTextOverBudgetKeepsChildren`, `dom.TestSerializedSizeNeverExceedsAccountedBytes`, `dom.TestParseChargesBytes` — decision 21 and 22 at the tree level; the serialized output of a hostile tree (escaping, minted ids) never exceeds the accounted bytes, and the cap holds.
- `jsvm.TestCreatedButNeverInsertedNodesAreReleased` — 500 `createElement` calls against a 60-node budget succeed and the tree returns to ≤ 10 live nodes after GC.
- `jsvm.TestFailedTextContentWriteKeepsChildrenAndListeners` — a `textContent` write that fails on the node cap leaves the children attached and a child's click listener firing.
- `jsvm.TestCloseDropsRuntime` — after `Close`, `Eval` and `Dispatch` return `ErrDisabled` and nothing can prevent a default action.
- `netpolicy.TestForbiddenIPCoversTransitionPrefixes` — NAT64/6to4/IPv4-compatible encodings of 169.254.169.254, 10.0.0.1 and 127.0.0.1, the local-use NAT64 range, Teredo, mapped forms and malformed addresses are refused; the same encodings of public addresses pass.
- `netpolicy.TestAnyOriginFollowsRedirectsButStillVetsAddresses` — a same-origin fetcher refuses a cross-port redirect; the navigation fetcher follows it; `AnyOrigin` without `AllowPrivate` still refuses NAT64 metadata, RFC1918, localhost, credentials, `file:` and `javascript:`.
- `loop.TestLatestLoadPageWins` — 50 rounds of two back-to-back loads through a one-slot inbox; the second always ends up current and never regresses.
- `loop.TestLinkActivatedMatchesNthtmlLinks` — `<a href="HTTP://example.test/A"><img alt=""></a>` and two same-href links, all with `preventDefault`: three listener hits, no `NavigateMsg`, duplicates resolved by label.
- `loop.TestDisabledPageIsDropped` — `for(;;){}` with one strike: the page is killed (runtime closed), its frame still renders, and a link on it still navigates.
- `app.TestViewNeverCarriesTerminalEscapes`, `app.TestHistoryMovesOnlyOnSuccessAndLatestNavigationWins`, `app.TestFirstFrameReflowsAtTerminalWidth` — decisions 7, 20 and 19 at the host: a 200-column terminal known before the first frame yields a 2000 px re-flow.
- `netpolicy.TestSameOriginIncludesPort` — `:8443` is not same-origin with `:443`; default port matches.
- `netpolicy.TestDeniesEverythingOffPolicy` — `file:`, `javascript:`, `data:`, localhost, RFC1918, link-local metadata, `[::1]`, `.internal`, credentials-in-URL, off-allowlist and suffix-spoofed hosts are denied; same-origin and allowlisted hosts pass.
- `netpolicy.TestBoundedBodyAndContentType` — oversize body → `ErrTooLarge`; `application/octet-stream` denied; `Set-Cookie` not exposed; `Cookie` request header dropped.
- `loop.TestLoopEndToEnd` — real page with inline script: `console.log` arrives sanitized, `requestAnimationFrame` fires with a numeric timestamp, `setTimeout` mutation shows in a rendered frame, `fetch().json()` against a loopback server mutates the DOM and renders, a link with `preventDefault` produces no `NavigateMsg`, a relative link produces a `NavigateMsg` resolved against the page base, and a subsequent `for(;;){}` trips the watchdog without stalling the loop.

Not verified: the Bubble Tea UI was not driven interactively (no TTY in this session); `app` is exercised headlessly through `Update` for frames, scroll, history, the confirm prompt text and the terminal boundary, not for key handling or a live `netpolicy` navigation. The heap-budget branch of `dropPage` shares its code with the tested strike branch but has no allocation-bomb test of its own. The heap watchdog has no test yet (it needs a controlled allocation bomb with a small `HeapBudget`).

Review rounds. (1) Builder check of v1: a fresh reviewer read the document and the pinned Goja/Bubble Tea sources; its confirmed findings (source-map bypass via `goja.Compile`, rAF never firing on a clean tree, `Program.Send` deadlock potential, relative links not resolving, `textContent` listener leak, `insertBefore` orphaning on error, watchdog race, same-origin ignoring port, search keystrokes leaking, `application/javascript` rejected, out-of-order external scripts) were fixed in v2. (2) Evan's external review of v3 (against nthtml v0.1.0, the Goja and Bubble Tea pins and the spike) found eleven issues; ten were confirmed against the code and fixed in v4 with the tests listed above: page text reaching the terminal unsanitized (decision 7), navigation fetched under the fetch allowlist and history moving before success (20), `preventDefault` missing nthtml's normalized links (§3, `anchorFor`), DOM lifetime gaps (21), memory ceilings not enforced (22), `LoadPage` ordering (23), NAT64/6to4/Teredo gaps (§9), stale pin sentences (§3), decision 13 contradicting 18, and §13 reading as open. One premise was checked and found wrong: nthtml's `SetBounds` *does* mutate `cols`/`rows` and the picture geometry synchronously (probed: `New` → `SetBounds(0,0,120,40)` gives `RasterGeometry().Pixels = (120,80)` before any `Cmd` runs), and the first-frame path already called it before `documentWidth`; what was missing was a re-check when the terminal's pixel geometry arrives after the first frame, so `app` now calls `loop.Resize(documentWidth(viewer))` on every frame (a no-op unless the width changed) and the 200-column startup case is tested.

## 11. nthtml: what v0.1.0 gave us, and what is still worth asking for (none blocking)

v0.1.0 landed before this document's requests were seen. Of the three v2 asks, `SetResult` shipped (with a view reset), `CellToDocument` did not but all of its inputs did, and `Link.Anchor` did not. ntbrowser v1 therefore needs **no nthtml change**. These are filed as upstream nice-to-haves, each with the host-side code it would replace:

1. `Model.SetResult` **preserving the view** (scroll, zoom, search query, focused link by `Link.ID`) — or a `SetResultKeepView`/option. Today `app` restores scroll via `Picture().Crop()` + `Pan`; zoom and search are lost on every re-flow because `zoom`/`query` are not readable. Replaces decision 17's restore code.
2. `func (m *Model) CellToDocument(p image.Point) (image.Rectangle, bool)` — export of `cellArea`/`rasterCellSource`. Replaces `app/cell.go` (≈60 lines) and its equivalence test; until then that test is the guard against drift between the two copies.
3. `document.Link.Anchor string` (the `<a>`'s `id`), or `LinkID` set on the `Tag:"anchor"` marker box. Replaces `loop.anchorFor`'s `href`+text match with an exact one.

Also noted, not requested: `Options.NoImage`/`Result.Paint` would let the loop lay out without painting, but `SetResult` then paints the whole page on the UI goroutine; a `Paint`-on-demand viewer would be the matching upstream change. `document.MinimalFonts` is a smaller embedded font source worth exposing as `-fonts minimal` for the wasm build later.

## 12. Left to the builder

- Taskfile, CI (native + `GOOS=js` compile of `dom`/`jsvm`/`loop`), `AGENTS.md`, `THIRD_PARTY_NOTICES` in the sibling-project style; `go.mod` requires `nimbleterminal.dev/nthtml v0.1.0`.
- Fuzz targets: `dom.Parse`+`Serialize` round-trip vs `document.Parse`; `netpolicy.Resolve`.
- Golden-file tests for the serializer, and a `-replay` flag that fixes clock and PRNG seeds.
- Optional `innerHTML` behind a flag, parsed by `dom.Parse` with the same limits, if real pages need it.
- A heap-watchdog test with a small `HeapBudget`.
- `app` is tested headlessly for frames, scroll restore and history only; drive key handling with `teatest` or a scripted `tea.Program`: navigation modes, the confirm prompt, search-box key isolation, Back/Forward keys.
- A tiny acceptance corpus under `examples/` (a counter with `setInterval`, a fetch-driven list, a rAF ticker, a link-interception page) so "done" is observable; the loop test's page is the seed.

## 13. Record of answers (nothing here is open)

Evan answered the four v1 questions on 2026-10-25; later that day nthtml v0.1.0 shipped and the design was re-based. The decisions above are normative; this section only records what was asked and answered so the scope is not reopened.

1. nthtml workstream first? — **Yes** at the time; **moot** since v0.1.0 shipped `SetResult` (decision 15). The first workstream is ntbrowser itself.
2. Navigation policy? — **Free by default, user/dev-configurable**: `-nav free|allowlist|confirm` (decisions 11, 20).
3. Target pages? — **Arbitrary small web pages**: removed nodes stay alive while referenced, element-level clicks and a focus model are in v1 (decisions 12, 13, 18, 21).
4. History in v1? — **Yes**: Back, Forward, Reload, always re-fetched, history moving only on a successful load (decisions 16, 20).

## Appendix A. Tests (verbatim from the spike)

`dom/dom_test.go`

```go
package dom

import (
	"errors"
	"strings"
	"testing"
)

func mustTree(t *testing.T, html string, l Limits) *Tree {
	t.Helper()
	tr, err := Parse(strings.NewReader(html), l)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// Every parentless node is reapable: created-and-dropped nodes count against
// the budget only until Release, exactly like removed subtrees.
func TestCreatedNodesAreDetachedUntilInserted(t *testing.T) {
	tr := mustTree(t, `<body></body>`, Limits{Nodes: 20})
	before := tr.Live()
	n, err := tr.NewElement("div")
	if err != nil {
		t.Fatal(err)
	}
	if roots := tr.DetachedRoots(); len(roots) != 1 || roots[0] != n {
		t.Fatalf("fresh node not reapable: %v", roots)
	}
	body, _ := tr.Query(tr.Root(), "body", true)
	if err := tr.Append(body[0], n); err != nil {
		t.Fatal(err)
	}
	if len(tr.DetachedRoots()) != 0 {
		t.Fatal("attached node still listed as detached")
	}
	tr.Detach(n)
	tr.Release(n)
	if tr.Live() != before {
		t.Fatalf("live %d after release, want %d", tr.Live(), before)
	}
	// Dropped without ever being inserted.
	m, _ := tr.NewElement("span")
	tr.Release(m)
	if tr.Live() != before || tr.Node(m.ID()) != nil {
		t.Fatal("never-inserted node was not released")
	}
}

func TestInsertBeforeSelfIsAMoveToTheSamePlace(t *testing.T) {
	tr := mustTree(t, `<body><p id="a"></p><p id="b"></p></body>`, Limits{})
	a, b := tr.ByID("a"), tr.ByID("b")
	body := a.Parent()
	if err := tr.InsertBefore(body, a, a); err != nil {
		t.Fatalf("insertBefore(node, node): %v", err)
	}
	if kids := body.Children(); len(kids) != 2 || kids[0] != a || kids[1] != b || a.Parent() != body {
		t.Fatalf("order changed or node orphaned: %v", kids)
	}
	if len(tr.DetachedRoots()) != 0 {
		t.Fatal("a node leaked into the detached set")
	}
	// A reference node from elsewhere is rejected without touching the child.
	other, _ := tr.NewElement("div")
	if err := tr.InsertBefore(body, b, other); err == nil || b.Parent() != body {
		t.Fatalf("bad ref: err=%v parent=%v", err, b.Parent())
	}
}

func TestSetTextOverBudgetKeepsChildren(t *testing.T) {
	tr := mustTree(t, `<body><div id="d"><b>x</b><i>y</i></div></body>`, Limits{Nodes: 12})
	d := tr.ByID("d")
	// Fill the remaining node budget so the replacement text node cannot be
	// allocated; the children must survive the failed write.
	for {
		n, err := tr.NewElement("span")
		if err != nil {
			break
		}
		_ = n
	}
	err := tr.SetText(d, "replacement")
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("want ErrLimit, got %v", err)
	}
	if len(d.Children()) != 2 || d.Children()[0].Parent() != d {
		t.Fatalf("failed write dropped the children: %v", d.Children())
	}
	// Clearing never needs a node.
	if err := tr.SetText(d, ""); err != nil || len(d.Children()) != 0 {
		t.Fatalf("clear: err=%v children=%v", err, d.Children())
	}
}

// The accounted size is an upper bound of the serializer's output, and the
// cap holds under worst-case escaping and minted ids.
func TestSerializedSizeNeverExceedsAccountedBytes(t *testing.T) {
	tr := mustTree(t, `<body></body>`, Limits{TotalBytes: 8 << 10})
	body, _ := tr.Query(tr.Root(), "body", true)
	hostile := strings.Repeat(`<>&"'`, 20)
	for {
		el, err := tr.NewElement("div")
		if err != nil {
			break
		}
		if err := tr.SetAttr(el, "title", hostile); err != nil {
			break
		}
		txt, err := tr.NewText(hostile)
		if err != nil {
			break
		}
		if err := tr.Append(body[0], el); err != nil {
			break
		}
		if err := tr.Append(el, txt); err != nil {
			break
		}
		tr.AddListener(el, "click") // forces a minted id in the output
	}
	out := tr.Serialize()
	if len(out) > tr.Bytes() {
		t.Fatalf("serialized %d bytes > accounted %d", len(out), tr.Bytes())
	}
	if tr.Bytes() > 8<<10 {
		t.Fatalf("accounted %d bytes over the 8 KiB cap", tr.Bytes())
	}
	if !strings.Contains(out, `id="nt-h-`) || !strings.Contains(out, "&lt;&gt;&amp;") {
		t.Fatal("output lacks the minted id or escaping the bound must cover")
	}
	// Releasing gives the budget back.
	for _, c := range body[0].Children() {
		tr.Detach(c)
		tr.Release(c)
	}
	if _, err := tr.NewText(hostile); err != nil {
		t.Fatalf("budget not returned after release: %v", err)
	}
}

func TestParseChargesBytes(t *testing.T) {
	_, err := Parse(strings.NewReader(`<p>`+strings.Repeat("x", 3000)+`</p>`), Limits{TotalBytes: 2048})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("oversize page parsed: %v", err)
	}
}
```

`jsvm/surface_test.go`

```go
package jsvm

import (
	"go/build"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"nimbleterminal.dev/ntbrowser/dom"
)

// TestNoSystemImports pins the hardened boundary: the packages that touch the
// VM must not import anything that can reach the OS. netpolicy and cmd are
// where those imports belong.
func TestNoSystemImports(t *testing.T) {
	forbidden := map[string]bool{"os": true, "os/exec": true, "net": true, "net/http": true, "syscall": true, "io/fs": true, "plugin": true, "unsafe": true}
	for _, pkg := range []string{"nimbleterminal.dev/ntbrowser/jsvm", "nimbleterminal.dev/ntbrowser/dom"} {
		p, err := build.Import(pkg, ".", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range p.Imports {
			if forbidden[imp] {
				t.Errorf("%s imports %s", pkg, imp)
			}
		}
	}
}

type nopHost struct{ lines []string }

func (h *nopHost) SetTimer(time.Duration, bool, goja.Callable, []goja.Value) (int, bool) {
	return 1, true
}
func (h *nopHost) ClearTimer(int)                                       {}
func (h *nopHost) RequestFrame(goja.Callable) int                       { return 1 }
func (h *nopHost) CancelFrame(int)                                      {}
func (h *nopHost) Fetch(FetchRequest, func(FetchResponse, error)) error { return nil }
func (h *nopHost) Log(level, line string)                               { h.lines = append(h.lines, level+": "+line) }

// TestGlobalSurface snapshots every global name scripts can see. A new name
// here is a review event: it means the boundary grew.
func TestGlobalSurface(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<p id=x>hi</p>"), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	names := s.vm.GlobalObject().GetOwnPropertyNames()
	sort.Strings(names)
	for _, n := range names {
		switch n {
		case "eval", "require", "process", "XMLHttpRequest", "WebSocket", "Worker", "SharedArrayBuffer", "Atomics", "importScripts", "WebAssembly":
			t.Errorf("forbidden global present: %s", n)
		}
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, want := range []string{"document", "fetch", "console", "setTimeout", "requestAnimationFrame", "location"} {
		if !have[want] {
			t.Errorf("missing host global %s", want)
		}
	}
}

func TestWatchdogAndStrikes(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<p></p>"), dom.Limits{})
	h := &nopHost{}
	s := New(Config{Slice: 20 * time.Millisecond, Strikes: 2}, tree, h)
	for i := 0; i < 2; i++ {
		if err := s.Eval("spin.js", "for(;;){}"); err == nil {
			t.Fatal("expected interrupt")
		}
	}
	if !s.Disabled() {
		t.Fatal("expected scripting disabled after strikes")
	}
	if err := s.Eval("after.js", "1"); err != ErrDisabled {
		t.Fatalf("got %v", err)
	}
}

func TestBridgeMutatesTreeNotPointers(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><p id=x>hi</p></body>"), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	src := `
		const p = document.getElementById("x");
		p.textContent = "<b>changed</b>";
		p.setAttribute("class", "note");
		const q = document.createElement("p");
		q.textContent = "two";
		document.body.appendChild(q);
		p.addEventListener("click", e => e.preventDefault());
		q.addEventListener("click", () => {}); // no id: serializer must mint one
		typeof p.__proto__.appendChild === "function" && Object.getOwnPropertySymbols(p).length === 1`
	if err := s.Eval("t.js", src); err != nil {
		t.Fatal(err)
	}
	html := tree.Serialize()
	for _, want := range []string{`<p id="x" class="note">&lt;b&gt;changed&lt;/b&gt;</p>`, `<p id="nt-h-`, `two</p>`} {
		if !strings.Contains(html, want) {
			t.Errorf("serialized html missing %q:\n%s", want, html)
		}
	}
	prevented, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("x").ID(), Href: "https://example.com"})
	if err != nil || !prevented {
		t.Fatalf("prevented=%v err=%v", prevented, err)
	}
	if !s.HasListeners("click") || s.HasListeners("keydown") {
		t.Fatal("listener bookkeeping wrong")
	}
}

func TestConsoleStripsTerminalEscapes(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader(""), dom.Limits{})
	h := &nopHost{}
	s := New(Config{}, tree, h)
	_ = s.Eval("c.js", `console.log("a\u001b]52;c;payload\u0007b", {k:1})`)
	if len(h.lines) != 1 || strings.ContainsRune(h.lines[0], 0x1b) || !strings.Contains(h.lines[0], `{"k":1}`) {
		t.Fatalf("unsafe or wrong log line: %q", h.lines)
	}
}

func TestTextContentSetterReleasesListeners(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><ul id=l></ul></body>"), dom.Limits{})
	s := New(Config{MaxListeners: 4}, tree, &nopHost{})
	src := `
		const ul = document.getElementById("l");
		for (let round = 0; round < 10; round++) {
			ul.textContent = "";               // frees previous children
			for (let i = 0; i < 3; i++) {
				const li = document.createElement("li");
				li.addEventListener("click", () => {});
				ul.appendChild(li);
			}
		}`
	if err := s.Eval("churn.js", src); err != nil {
		t.Fatalf("listener budget leaked across textContent resets: %v", err)
	}
	if s.nListen != 3 {
		t.Fatalf("nListen=%d want 3", s.nListen)
	}
}
```

`jsvm/sourcemap_test.go`

```go
package jsvm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dop251/goja"

	"nimbleterminal.dev/ntbrowser/dom"
)

// TestSourceMapCommentNeverTouchesFilesystem shows the only observable of
// Goja's default source-map loader (os.ReadFile on a page-controlled
// file:// path; errors are swallowed, data only remaps stack positions) and
// proves the sandbox's compile path does not exhibit it.
//
// Control: compiling the same script with goja.Compile *does* read the map
// (the thrown error's frame is remapped to SECRET_SOURCE.js). If the control
// ever stops observing the remap, this test fails so it cannot silently turn
// into a no-op.
func TestSourceMapCommentNeverTouchesFilesystem(t *testing.T) {
	mapPath := filepath.Join(t.TempDir(), "page.js.map")
	const cols = "AAAA,CAAC,CAAC,CAAC,CAAC,CAAC,CAAC,CAAC,CAAC,CAAC"
	if err := os.WriteFile(mapPath, []byte(`{"version":3,"sources":["SECRET_SOURCE.js"],"names":[],"mappings":"`+strings.Repeat(cols+";", 3)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Goja resolves a bare absolute path relative to the script *name* (which
	// for ntbrowser is an https URL, making the scheme "unsupported"), but a
	// file:// URL is used verbatim: that is the live vector.
	src := "\nthrow new Error('boom')\n//# sourceMappingURL=file://" + mapPath + "\n"

	srcName := func(err error) string {
		var ex *goja.Exception
		if !errors.As(err, &ex) || len(ex.Stack()) == 0 {
			t.Fatalf("expected a JS exception with a stack, got %v", err)
		}
		return ex.Stack()[0].Position().Filename // Position() applies the map; SrcName() never does
	}

	// Control: the unhardened path reads the map file.
	prg, err := goja.Compile("page.js", src, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = goja.New().RunProgram(prg)
	if got := srcName(err); !strings.Contains(got, "SECRET_SOURCE.js") {
		t.Fatalf("control failed: goja.Compile did not apply the source map (src=%q); the oracle is broken", got)
	}

	// Sandbox path: same script, map must be ignored.
	tree, _ := dom.Parse(strings.NewReader(""), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	err = s.Eval("page.js", src)
	var ex *goja.Exception
	if !errors.As(err, &ex) {
		// Sandbox.Run wraps exceptions; unwrap by re-running through the raw path
		// of the sandbox's own program to inspect the frame.
		ast, perr := goja.Parse("page.js", src, sandboxParserOptions()...)
		if perr != nil {
			t.Fatal(perr)
		}
		prg, cerr := goja.CompileAST(ast, false)
		if cerr != nil {
			t.Fatal(cerr)
		}
		_, err = s.vm.RunProgram(prg)
	}
	if got := srcName(err); strings.Contains(got, "SECRET_SOURCE.js") {
		t.Fatalf("sandbox applied a page-supplied source map: src=%q", got)
	}
}
```

`jsvm/compilepath_test.go`

```go
package jsvm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyHardenedCompilePath statically forbids every Goja entry point that
// parses source without our parser options. goja.Compile passes nil options
// (runtime.go: compile(name, src, strict, true, nil)); RunString/RunScript
// use the runtime's options but would make the rule harder to audit. The
// only allowed path is goja.Parse(name, src, parser.WithDisableSourceMaps)
// followed by goja.CompileAST, in Sandbox.Eval.
func TestOnlyHardenedCompilePath(t *testing.T) {
	forbidden := map[string]bool{"Compile": true, "MustCompile": true, "RunString": true, "RunScript": true}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		parseCalls := 0
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if forbidden[sel.Sel.Name] {
				t.Errorf("%s: forbidden compile path %s", fset.Position(call.Pos()), sel.Sel.Name)
			}
			if sel.Sel.Name == "Parse" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "goja" {
					parseCalls++
					if len(call.Args) < 3 {
						t.Errorf("%s: goja.Parse without parser options", fset.Position(call.Pos()))
					}
				}
			}
			return true
		})
		_ = parseCalls
	}
}
```

`jsvm/lifetime_test.go`

```go
package jsvm

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/dom"
)

// Browser semantics: a removed node stays alive while referenced and can be
// re-inserted with its listeners intact.
func TestDetachKeepsNodeAndListenersAlive(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><ul id=l><li id=a>a</li></ul></body>"), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	src := `
		const li = document.getElementById("a");
		let clicks = 0;
		li.addEventListener("click", () => { clicks++; });
		li.remove();
		if (li.isConnected) throw new Error("still connected");
		if (li.textContent !== "a") throw new Error("detached node unusable");
		document.getElementById("l").appendChild(li);   // re-insert
		if (!li.isConnected) throw new Error("not reconnected");
		globalThis.readClicks = () => clicks;`
	if err := s.Eval("t.js", src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("a").ID()}); err != nil {
		t.Fatal(err)
	}
	v, err := s.vm.RunString("readClicks()")
	if err != nil {
		t.Fatalf("readClicks(): %v", err)
	}
	if v.ToInteger() != 1 {
		t.Fatalf("listener lost across detach/attach: clicks=%d", v.ToInteger())
	}
}

// Detached subtrees that JavaScript no longer references are released from
// the node budget once the GC reports their wrappers.
func TestDetachedSubtreesAreReleasedAfterGC(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><div id=root></div></body>"), dom.Limits{Nodes: 400})
	s := New(Config{}, tree, &nopHost{})
	build := `
		const root = document.getElementById("root");
		for (let i = 0; i < 100; i++) {
			const d = document.createElement("div");
			d.addEventListener("click", () => {});
			const t = document.createTextNode("x" + i);
			d.appendChild(t);
			root.appendChild(d);
		}
		root.textContent = "";   // detach all 200 nodes; no JS references remain`
	if err := s.Eval("build.js", build); err != nil {
		t.Fatal(err)
	}
	before := tree.Live()
	deadline := time.Now().Add(5 * time.Second)
	for tree.Live() > 10 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
		s.Reap()
	}
	if tree.Live() > 10 {
		t.Fatalf("detached nodes not released: live=%d (was %d)", tree.Live(), before)
	}
	if s.nListen != 0 || s.HasListeners("click") {
		t.Fatalf("listener counts not reconciled: nListen=%d", s.nListen)
	}
	// And the budget is usable again.
	if err := s.Eval("again.js", `for (let i = 0; i < 150; i++) document.getElementById("root").appendChild(document.createElement("p"))`); err != nil {
		t.Fatal(err)
	}
}

// A listener on an attached element keeps firing even when the page dropped
// every reference to the element.
func TestAttachedListenerSurvivesWrapperDrop(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><p id=p>hi</p></body>"), dom.Limits{})
	h := &nopHost{}
	s := New(Config{}, tree, h)
	if err := s.Eval("t.js", `(() => { const p = document.getElementById("p"); p.addEventListener("click", () => console.log("fired")); })()`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("p").ID()}); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 1 || !strings.Contains(h.lines[0], "fired") {
		t.Fatalf("listener did not fire after GC: %q", h.lines)
	}
}

func TestFocusRoutesKeydown(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><button id=b>go</button><p id=p>x</p></body>"), dom.Limits{})
	h := &nopHost{}
	s := New(Config{}, tree, h)
	src := `
		document.getElementById("b").addEventListener("keydown", e => console.log("button got", e.key));
		document.addEventListener("keydown", e => console.log("document got", e.key, e.target === document.getElementById("b")));
		document.getElementById("b").focus();
		if (document.activeElement.id !== "b") throw new Error("focus() did not move activeElement");`
	if err := s.Eval("t.js", src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(Event{Type: "keydown", Key: "Enter"}); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 2 || !strings.Contains(h.lines[0], "button got Enter") || !strings.Contains(h.lines[1], "document got Enter true") {
		t.Fatalf("keydown routing wrong: %q", h.lines)
	}
	// A non-focusable click target clears focus back to the body.
	s.Focus(tree.ByID("p").ID())
	if s.Active() != 0 {
		t.Fatal("non-focusable element took focus")
	}
}

// Nodes created by the page and never inserted are released once their
// wrappers are collected, so a create-and-drop loop cannot pin the budget.
func TestCreatedButNeverInsertedNodesAreReleased(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body></body>"), dom.Limits{Nodes: 60})
	s := New(Config{}, tree, &nopHost{})
	// 500 creations against a 60-node budget only pass if the bridge's forced
	// collection reclaims dropped wrappers along the way.
	if err := s.Eval("churn.js", `for (let i = 0; i < 500; i++) { document.createElement("div"); }`); err != nil {
		t.Fatalf("create-and-drop exhausted the budget: %v", err)
	}
	base := tree.Live()
	deadline := time.Now().Add(5 * time.Second)
	for tree.Live() > 10 && time.Now().Before(deadline) {
		runtime.GC()
		s.Reap()
	}
	if tree.Live() > 10 {
		t.Fatalf("live nodes %d (was %d) after GC: never-inserted nodes not released", tree.Live(), base)
	}
}

// A textContent write that fails on the budget leaves the children attached
// and their listeners firing (the setter is transactional end to end).
func TestFailedTextContentWriteKeepsChildrenAndListeners(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><div id=d><b id=k>x</b></div></body>"), dom.Limits{Nodes: 12})
	s := New(Config{}, tree, &nopHost{})
	src := `
		globalThis.hits = 0;
		const k = document.getElementById("k");
		k.addEventListener("click", () => { globalThis.hits++; });
		const keep = [];
		for (;;) { try { keep.push(document.createElement("i")); } catch (e) { break; } } // fill the budget, keep refs
		let threw = false;
		try { document.getElementById("d").textContent = "replacement"; } catch (e) { threw = true; }
		if (!threw) throw new Error("write over budget did not throw");
		if (!k.isConnected) throw new Error("child detached by a failed write");`
	if err := s.Eval("t.js", src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("k").ID()}); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.vm.RunString("hits"); v.ToInteger() != 1 {
		t.Fatalf("listener lost after failed write: hits=%d", v.ToInteger())
	}
}

// Close drops the runtime: scripts can no longer run, and a click on a link
// is reported as not prevented so navigation still works for the reader.
func TestCloseDropsRuntime(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader(`<body><a id=a href="/x">x</a></body>`), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	if err := s.Eval("t.js", `document.getElementById("a").addEventListener("click", e => e.preventDefault())`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !s.Disabled() {
		t.Fatal("not disabled")
	}
	prevented, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("a").ID()})
	if prevented || err != ErrDisabled {
		t.Fatalf("after Close: prevented=%v err=%v", prevented, err)
	}
	if err := s.Eval("again.js", `1`); err != ErrDisabled {
		t.Fatalf("Eval after Close: %v", err)
	}
}
```

`netpolicy/policy_test.go`

```go
package netpolicy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDeniesEverythingOffPolicy(t *testing.T) {
	origin, _ := url.Parse("https://docs.example.com/guide/")
	f := New(Policy{Origin: origin, AllowHosts: []string{".cdn.example.net"}})
	for _, ref := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "data:text/html,hi",
		"http://localhost/", "http://127.0.0.1:8080/", "http://10.0.0.5/", "http://169.254.169.254/latest/meta-data",
		"http://[::1]/", "http://metadata.internal/", "https://user:pw@docs.example.com/",
		"https://other.example.com/", "https://evil.cdn.example.net.attacker.com/", "https://docs.example.com/x y",
	} {
		if _, err := f.Resolve(ref); !errors.Is(err, ErrDenied) && err == nil {
			t.Errorf("%s: expected denial, got %v", ref, err)
		}
	}
	for _, ref := range []string{"/api/data.json", "../sibling.html", "https://docs.example.com/other", "https://img.cdn.example.net/a.json"} {
		if _, err := f.Resolve(ref); err != nil {
			t.Errorf("%s: expected allow, got %v", ref, err)
		}
	}
}

func TestBoundedBodyAndContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(strings.Repeat("x", 5000)))
		case "/bin":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte{0, 1, 2})
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Set-Cookie", "secret=1")
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()
	origin, _ := url.Parse(srv.URL)
	f := New(Policy{Origin: origin, AllowPrivate: true, MaxBody: 4096})
	if _, err := f.Do(context.Background(), Request{URL: "/big"}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big: %v", err)
	}
	if _, err := f.Do(context.Background(), Request{URL: "/bin"}); !errors.Is(err, ErrDenied) {
		t.Errorf("bin: %v", err)
	}
	resp, err := f.Do(context.Background(), Request{URL: "/ok", Headers: map[string]string{"Cookie": "x=1", "Accept": "application/json"}})
	if err != nil || resp.Status != 200 || resp.Body != `{"ok":true}` {
		t.Fatalf("ok: %+v %v", resp, err)
	}
	if _, leaked := resp.Headers["set-cookie"]; leaked {
		t.Error("Set-Cookie leaked to the page")
	}
}

func TestSameOriginIncludesPort(t *testing.T) {
	origin, _ := url.Parse("https://host.example/")
	f := New(Policy{Origin: origin})
	if _, err := f.Resolve("https://host.example:8443/x"); !errors.Is(err, ErrDenied) {
		t.Fatalf("port differs, expected denial: %v", err)
	}
	if _, err := f.Resolve("https://host.example:443/x"); err != nil {
		t.Fatalf("default port should match: %v", err)
	}
}
```

`netpolicy/transition_test.go`

```go
package netpolicy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Transition-prefix forms of forbidden IPv4 addresses are forbidden too; the
// same forms of public addresses are not (NAT64-only networks must work).
func TestForbiddenIPCoversTransitionPrefixes(t *testing.T) {
	deny := []string{
		"64:ff9b::a9fe:a9fe",   // NAT64 of 169.254.169.254
		"64:ff9b::a00:1",       // NAT64 of 10.0.0.1
		"64:ff9b::7f00:1",      // NAT64 of 127.0.0.1
		"64:ff9b:1::a9fe:a9fe", // local-use NAT64, any payload
		"64:ff9b:1::808:808",
		"2002:a9fe:a9fe::1",                    // 6to4 of 169.254.169.254
		"2002:0a00:0001::1",                    // 6to4 of 10.0.0.1
		"2001:0:4136:e378:8000:63bf:3fff:fdd2", // Teredo
		"::a9fe:a9fe",                          // IPv4-compatible 169.254.169.254
		"::ffff:169.254.169.254",
		"169.254.169.254", "10.0.0.1", "100.64.0.1", "127.0.0.1", "0.0.0.0", "192.0.0.1", "::1", "fe80::1", "fc00::1",
	}
	allow := []string{"64:ff9b::808:808", "2002:808:808::1", "::ffff:8.8.8.8", "8.8.8.8", "2606:4700::1111"}
	for _, s := range deny {
		if !isForbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be forbidden", s)
		}
	}
	for _, s := range allow {
		if isForbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
	if !isForbiddenIP(nil) || !isForbiddenIP(net.IP{1, 2, 3}) {
		t.Error("malformed addresses must be forbidden")
	}
}

// AnyOrigin (top-level navigation) admits other hosts and follows redirects
// across scheme and host, but keeps every other check.
func TestAnyOriginFollowsRedirectsButStillVetsAddresses(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<p>landed</p>"))
	}))
	defer final.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/landing", http.StatusFound)
	}))
	defer first.Close()
	origin, _ := url.Parse(first.URL)

	// Same-origin policy (a page's fetch): the cross-port redirect is denied.
	strict := New(Policy{Origin: origin, AllowPrivate: true})
	if _, err := strict.Do(t.Context(), Request{Method: "GET", URL: first.URL + "/go"}); err == nil {
		t.Fatal("same-origin fetcher followed a redirect to another origin")
	}
	// Navigation policy: followed.
	nav := New(Policy{Origin: origin, AllowPrivate: true, AnyOrigin: true})
	resp, err := nav.Do(t.Context(), Request{Method: "GET", URL: first.URL + "/go"})
	if err != nil || resp.Body != "<p>landed</p>" {
		t.Fatalf("navigation did not follow the redirect: %v %q", err, resp.Body)
	}
	if u, _ := url.Parse(resp.URL); u.Host != origin.Host && u.Path != "/landing" {
		t.Fatalf("final URL not reported: %s", resp.URL)
	}

	// Without AllowPrivate, AnyOrigin still refuses forbidden targets and
	// everything that is not http(s).
	pub := New(Policy{AnyOrigin: true})
	for _, raw := range []string{"http://[64:ff9b::a9fe:a9fe]/", "http://10.0.0.1/", "http://localhost/", "http://user:pw@example.com/", "file:///etc/passwd", "javascript:alert(1)"} {
		u, _ := url.Parse(raw)
		if err := pub.Allowed(u); err == nil {
			t.Errorf("AnyOrigin admitted %s", raw)
		}
	}
	u, _ := url.Parse("https://example.com/anything")
	if err := pub.Allowed(u); err != nil {
		t.Errorf("AnyOrigin refused a public host: %v", err)
	}
}
```

`loop/loop_test.go`

```go
package loop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/netpolicy"
)

type chanSink chan tea.Msg

func (c chanSink) Send(m tea.Msg) { c <- m }

const page = `<!doctype html><html><head><title>Loop test</title></head><body>
<h1 id="h">Hello</h1><p id="p">waiting</p><a id="lnk" href="https://example.com/next">next</a>
<a id="rel" href="../other.html">other</a>
<script>
  document.getElementById("lnk").addEventListener("click", e => e.preventDefault());
  requestAnimationFrame(ts => console.log("raf", typeof ts === "number" && ts >= 0));
  setTimeout(() => { document.getElementById("h").textContent = "Tick"; }, 5);
  fetch("/data.json").then(r => r.json()).then(d => {
    document.getElementById("p").textContent = "got " + d.value;
  }).catch(e => console.error("fetch failed", String(e)));
  console.log("booted", 1 + 1);
</script></body></html>`

func TestLoopEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value":42}`))
	}))
	defer srv.Close()
	origin, _ := url.Parse(srv.URL + "/page.html")
	fetcher := netpolicy.New(netpolicy.Policy{Origin: origin, AllowPrivate: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	l := New(ctx, Config{Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(page, origin.String(), fetcher)

	var sawTitle, sawBoot, sawTick, sawFetch, sawRAF bool
	deadline := time.After(5 * time.Second)
	for !(sawTitle && sawBoot && sawTick && sawFetch && sawRAF) {
		select {
		case <-deadline:
			t.Fatalf("timeout: title=%v boot=%v tick=%v fetch=%v raf=%v", sawTitle, sawBoot, sawTick, sawFetch, sawRAF)
		case m := <-sink:
			switch v := m.(type) {
			case TitleMsg:
				sawTitle = v.Title == "Loop test"
			case LogMsg:
				if v.Level == "error" {
					t.Fatalf("page error: %s", v.Line)
				}
				sawBoot = sawBoot || v.Line == "booted 2"
				sawRAF = sawRAF || v.Line == "raf true"
			case FrameMsg:
				if v.Err != nil {
					t.Fatal(v.Err)
				}
				text, _ := v.Result.TTY(ctx, 60)
				sawTick = sawTick || strings.Contains(text, "Tick")
				sawFetch = sawFetch || strings.Contains(text, "got 42")
			}
		}
	}
	// Link default action is cancelled by the page's listener …
	l.LinkActivated(mustLink(t, l))
	select {
	case m := <-sink:
		if _, nav := m.(NavigateMsg); nav {
			t.Fatal("navigation should have been prevented")
		}
	case <-time.After(200 * time.Millisecond):
	}
	// A relative link resolves against the page base before it reaches the host.
	l.LinkActivated(document_Link{Href: "../other.html", Text: "other"})
	select {
	case m := <-sink:
		nav, ok := m.(NavigateMsg)
		if !ok || nav.Href != srv.URL+"/other.html" {
			t.Fatalf("expected resolved NavigateMsg, got %#v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no NavigateMsg for relative link")
	}
	// … but a hostile script cannot block the terminal: the watchdog trips.
	l.post(func() { l.report(l.page.Sandbox.Eval("spin", "for(;;){}")) })
	select {
	case m := <-sink:
		if lm, ok := m.(LogMsg); !ok || !strings.Contains(lm.Line, "time slice") {
			t.Fatalf("expected watchdog log, got %#v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not fire")
	}
}

func mustLink(t *testing.T, l *Loop) (link document_Link) {
	done := make(chan document_Link)
	l.post(func() {
		anchors, _ := l.page.Tree.Query(l.page.Tree.Root(), "a", true)
		href, _ := anchors[0].Attr("href")
		done <- document_Link{Href: href, Text: "next"}
	})
	return <-done
}
```

`loop/click_test.go`

```go
package loop

import (
	"context"
	"image"
	"testing"
	"time"
)

const clickPage = `<!doctype html><html><body>
<div id="card" style="padding:8px"><p>Card body text</p></div>
<div style="padding:8px"><p>A div with a listener but no id</p></div>
<a href="/go">go</a>
<script>
  document.getElementById("card").addEventListener("click", e => console.log("card click", e.target.id));
  document.querySelectorAll("div")[1].addEventListener("click", e => { console.log("anon click", e.target.tagName); });
</script></body></html>`

// Element-level clicks: a document-pixel area is mapped to the smallest
// anchored box, which the serializer guarantees for listener-bearing
// elements (synthetic id), then dispatched with bubbling.
func TestClickAtHitsElementsAndMintedAnchors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	l := New(ctx, Config{Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(clickPage, "https://example.test/", nil)

	var frame FrameMsg
	deadline := time.After(5 * time.Second)
	for frame.Result == nil {
		select {
		case <-deadline:
			t.Fatal("no frame")
		case m := <-sink:
			if f, ok := m.(FrameMsg); ok && f.Err == nil {
				frame = f
			}
		}
	}
	var card, anon image.Rectangle
	for _, b := range frame.Result.Boxes {
		switch {
		case b.Anchor == "card":
			card = b.Bounds
		case len(b.Anchor) > 5 && b.Anchor[:5] == "nt-h-":
			anon = b.Bounds
		}
	}
	if card.Empty() || anon.Empty() {
		t.Fatalf("anchored boxes missing: card=%v anon=%v", card, anon)
	}
	// Click inside the card's paragraph: the <p> has no anchor, the card does.
	l.ClickAt(image.Rect(card.Min.X+10, card.Min.Y+10, card.Min.X+11, card.Min.Y+11))
	l.ClickAt(image.Rect(anon.Min.X+10, anon.Min.Y+10, anon.Min.X+11, anon.Min.Y+11))
	want := map[string]bool{"card click card": false, "anon click DIV": false}
	deadline = time.After(3 * time.Second)
	for {
		done := true
		for _, ok := range want {
			done = done && ok
		}
		if done {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("missing click logs: %v", want)
		case m := <-sink:
			if lm, ok := m.(LogMsg); ok {
				if _, exp := want[lm.Line]; exp {
					want[lm.Line] = true
				} else if lm.Level == "error" {
					t.Fatalf("page error: %s", lm.Line)
				}
			}
		}
	}
}
```

`loop/order_test.go`

```go
package loop

import (
	"context"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/jsvm"
)

func pageState(t *testing.T, l *Loop) (base string, killed, disabled bool) {
	t.Helper()
	done := make(chan struct{})
	l.post(func() {
		if l.page != nil {
			base, killed, disabled = l.page.Base, l.page.killed, l.page.Sandbox.Disabled()
		}
		close(done)
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not answer")
	}
	return
}

// Two LoadPage calls in a row: the second always wins, whatever order the
// handoffs reach the VM goroutine in.
func TestLatestLoadPageWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := New(ctx, Config{Width: 200, Inbox: 1}, make(chanSink, 4096)) // tiny inbox forces the goroutine handoff path
	go l.Run()
	for i := 0; i < 50; i++ {
		l.LoadPage(`<title>A</title><p>a</p>`, "https://a.test/", nil)
		l.LoadPage(`<title>B</title><p>b</p>`, "https://b.test/", nil)
		// A may legitimately apply before B's call was even made; B must
		// apply eventually, and once it has, A must never apply again.
		deadline := time.Now().Add(5 * time.Second)
		for {
			base, _, _ := pageState(t, l)
			if base == "https://b.test/" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("round %d: B never applied (page=%q)", i, base)
			}
		}
		for k := 0; k < 3; k++ {
			if base, _, _ := pageState(t, l); base != "https://b.test/" {
				t.Fatalf("round %d: stale page won after B: %s", i, base)
			}
		}
	}
}

// preventDefault must see the link nthtml reports: href normalized the way
// nthtml normalizes it, label empty (image link) so Link.Text is the href.
func TestLinkActivatedMatchesNthtmlLinks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Width: 400, MinFrame: time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(`<!doctype html><body>
<a href="HTTP://example.test/A"><img alt=""></a>
<a href="/dup">one</a> <a href="/dup">two</a>
<script>
  for (const a of document.querySelectorAll("a")) a.addEventListener("click", e => { e.preventDefault(); console.log("prevented " + a.textContent); });
</script></body>`, "https://example.test/", nil)
	var frame FrameMsg
	deadline := time.After(5 * time.Second)
	for frame.Result == nil {
		select {
		case <-deadline:
			t.Fatal("no frame")
		case m := <-sink:
			if f, ok := m.(FrameMsg); ok && f.Err == nil {
				frame = f
			}
		}
	}
	if len(frame.Result.Links) != 3 {
		t.Fatalf("nthtml links: %+v", frame.Result.Links)
	}
	for _, link := range frame.Result.Links {
		l.LinkActivated(link)
	}
	prevented := 0
	timeout := time.After(2 * time.Second)
	for prevented < 3 {
		select {
		case <-timeout:
			t.Fatalf("only %d of 3 link clicks reached their listeners", prevented)
		case m := <-sink:
			switch v := m.(type) {
			case NavigateMsg:
				t.Fatalf("navigation not prevented: %s", v.Href)
			case LogMsg:
				if v.Level == "error" {
					t.Fatalf("page error: %s", v.Line)
				}
				if len(v.Line) > 9 && v.Line[:9] == "prevented" {
					prevented++
				}
			}
		}
	}
	// Duplicate hrefs resolve by label.
	for _, link := range frame.Result.Links {
		if link.Href == "/dup" && l.pageAnchorText(link) != link.Text {
			t.Fatalf("duplicate href %q resolved to the wrong element: %q", link.Text, l.pageAnchorText(link))
		}
	}
}

func (l *Loop) pageAnchorText(link document_Link) (text string) {
	done := make(chan struct{})
	l.post(func() {
		if n := l.anchorFor(link); n != nil {
			text = dom.TextContent(n)
		}
		close(done)
	})
	<-done
	return
}

// A page that exhausts its strike budget loses its runtime but not its
// frame: links still open, scripts never run again.
func TestDisabledPageIsDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Width: 200, Script: jsvm.Config{Slice: 5 * time.Millisecond, Strikes: 1}}, sink)
	go l.Run()
	l.LoadPage(`<body><a href="/go">go</a><script>
	  document.querySelector("a").addEventListener("click", e => e.preventDefault());
	  for(;;){}
	</script></body>`, "https://example.test/", nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, killed, disabled := pageState(t, l)
		if killed && disabled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("page not dropped: killed=%v disabled=%v", killed, disabled)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var frame FrameMsg
	timeout := time.After(5 * time.Second)
	for frame.Result == nil {
		select {
		case <-timeout:
			t.Fatal("no frame for the dropped page")
		case m := <-sink:
			if f, ok := m.(FrameMsg); ok && f.Err == nil {
				frame = f
			}
		}
	}
	l.LinkActivated(frame.Result.Links[0])
	timeout = time.After(2 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("link on a dropped page did not navigate")
		case m := <-sink:
			if n, ok := m.(NavigateMsg); ok {
				if n.Href != "https://example.test/go" {
					t.Fatalf("navigated to %s", n.Href)
				}
				return
			}
		}
	}
}
```

`loop/alias_test.go`

```go
package loop

import "nimbleterminal.dev/nthtml/document"

type document_Link = document.Link
```

`app/cell_test.go`

```go
package app

import (
	"context"
	"image"
	"strings"
	"testing"

	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
)

const cellPage = `<!doctype html><html><body style="font-size:16px">
<p><a href="/one">first link on the first line</a></p>
<p style="text-align:right"><a href="/two">right-aligned</a></p>
<div style="height:600px"></div>
<p><a href="/three">a link far below the fold</a> plain text after it</p>
<div style="height:600px"></div>
</body></html>`

// The host's cell→document mapping must agree with nthtml's own, which is
// only reachable through Model.LinkAtCell: wherever nthtml reports a link
// under a cell, the host's area overlaps that link's regions, and wherever
// the host's area overlaps a link region, nthtml reports a link. Checked at
// a non-zero widget origin, and again after scrolling.
func TestCellToDocumentAgreesWithLinkAtCell(t *testing.T) {
	res, err := document.Render(context.Background(), strings.NewReader(cellPage), document.Options{Width: 400})
	if err != nil {
		t.Fatal(err)
	}
	m := nthtml.New(res)
	_ = m.Init()
	_ = m.SetBounds(3, 2, 80, 24)
	overlaps := func(area image.Rectangle, l document.Link) bool {
		for _, r := range l.Regions {
			if !r.Intersect(area).Empty() {
				return true
			}
		}
		return false
	}
	check := func(label string) (hits int) {
		for y := 0; y < 30; y++ {
			for x := 0; x < 90; x++ {
				p := image.Pt(x, y)
				link, hasLink := m.LinkAtCell(p)
				area, ok := cellToDocument(m, p)
				if hasLink {
					hits++
					if !ok || !overlaps(area, link) {
						t.Fatalf("%s: cell %v: nthtml sees %q, host area %v (ok=%v) misses it", label, p, link.Href, area, ok)
					}
					continue
				}
				if !ok {
					continue
				}
				for _, l := range res.Links {
					if overlaps(area, l) {
						t.Fatalf("%s: cell %v: host area %v overlaps %q but nthtml sees no link", label, p, area, l.Href)
					}
				}
			}
		}
		return hits
	}
	if n := check("top"); n == 0 {
		t.Fatal("no link cells at the top of the page; test is vacuous")
	}
	_ = m.Pan(0, 700)
	if n := check("scrolled"); n == 0 {
		t.Fatal("no link cells after scrolling; test is vacuous")
	}
	if _, ok := cellToDocument(m, image.Pt(0, 0)); ok {
		t.Fatal("cell outside the widget origin mapped to a document area")
	}
	if w := documentWidth(m); w != 800 {
		t.Fatalf("glyph-fallback geometry should give 10px per column (800), got %d", w)
	}
}
```

`app/app_test.go`

```go
package app

import (
	"context"
	"net/url"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
)

type dropSink struct{}

func (dropSink) Send(tea.Msg) {}

func renderTall(t *testing.T, body string) *document.Result {
	t.Helper()
	res, err := document.Render(context.Background(), strings.NewReader(
		`<!doctype html><html><body>`+body+`<div style="height:3000px"></div></body></html>`), document.Options{Width: 400})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A re-flow of the same page keeps the reader's scroll offset; the first
// frame of a new page starts at the top. Both go through the same FrameMsg
// path, distinguished only by pageLoadedMsg having arrived in between.
func TestReflowKeepsScrollAndNewPageResetsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Width: 400}, dropSink{})
	go l.Run()
	m := New(l, netpolicy.Policy{}, NavFree, "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})

	first := renderTall(t, `<p>v1</p>`)
	m.Update(loop.FrameMsg{Gen: 1, Result: first})
	if m.viewer == nil {
		t.Fatal("no viewer after first frame")
	}
	_ = m.viewer.Pan(0, 900)
	if y := m.viewer.Picture().Crop().Min.Y; y != 900 {
		t.Fatalf("pan did not take: crop y=%d", y)
	}

	m.Update(loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p>v2 (script mutated the page)</p>`)})
	if y := m.viewer.Picture().Crop().Min.Y; y != 900 {
		t.Fatalf("re-flow lost the scroll offset: crop y=%d", y)
	}
	if m.viewer.Result().Document() == first.Document() {
		t.Fatal("viewer still shows the old Result")
	}

	// A stale frame (older generation) never regresses the view.
	m.Update(loop.FrameMsg{Gen: 1, Result: first})
	if m.viewer.Result().Document() == first.Document() {
		t.Fatal("stale frame was applied")
	}

	// New page: pageLoadedMsg precedes its first frame.
	m.Update(pageLoadedMsg{html: "<p>next</p>", base: mustURL(t, "https://example.test/next"), seq: m.navSeq, hist: pushNew})
	m.Update(loop.FrameMsg{Gen: 3, Result: renderTall(t, `<p>next page</p>`)})
	if y := m.viewer.Picture().Crop().Min.Y; y != 0 {
		t.Fatalf("new page did not start at the top: crop y=%d", y)
	}
	if m.hIndex != 0 || len(m.history) != 1 {
		t.Fatalf("history not pushed: %v @%d", m.history, m.hIndex)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
```

`app/boundary_test.go`

```go
package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
)

func hasTerminalControls(s string) bool {
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if unsafeRune(r) {
			return true
		}
	}
	return false
}

// Page-controlled text (title, log lines, error strings, hrefs in the
// confirm prompt) never reaches the terminal with controls in it.
func TestViewNeverCarriesTerminalEscapes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Width: 400}, dropSink{})
	go l.Run()
	m := New(l, netpolicy.Policy{}, NavConfirm, "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	osc52 := "\x1b]52;c;SGVsbG8=\x07"
	m.Update(loop.TitleMsg{Title: "Owned" + osc52 + "\x9d title   line"})
	m.Update(loop.LogMsg{Level: "error", Line: "x\x1b[31mred"})
	m.Update(pageLoadedMsg{seq: m.navSeq, err: errors.New("parse \"" + osc52 + "\": bad")})
	for _, probe := range []string{m.title, m.status} {
		if hasTerminalControls(probe) {
			t.Fatalf("stored unsanitized: %q", probe)
		}
	}
	if out := m.View().Content; hasTerminalControls(out) || !strings.Contains(out, "Owned") {
		t.Fatalf("View carries controls or lost the title: %q", out)
	}
	m.current = mustURL(t, "https://example.test/")
	// A link whose href carries controls is refused by url.Parse; the error
	// quotes the input, and the quoted form must still be free of controls.
	m.Update(loop.NavigateMsg{Href: "https://evil.test/" + osc52})
	if hasTerminalControls(m.status) || !strings.HasPrefix(m.status, "bad link:") {
		t.Fatalf("bad-link status: %q", m.status)
	}
	// A parseable off-origin link is shown in the confirm prompt, cleaned.
	m.Update(loop.NavigateMsg{Href: "https://evil.test/\u0085path"})
	if hasTerminalControls(m.status) || !strings.Contains(m.status, "Open https://evil.test/") {
		t.Fatalf("confirm prompt: %q", m.status)
	}
}

// Back/Forward only move when the load succeeds, and a superseded load
// never lands.
func TestHistoryMovesOnlyOnSuccessAndLatestNavigationWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Width: 400}, dropSink{})
	go l.Run()
	m := New(l, netpolicy.Policy{}, NavFree, "")
	m.history, m.hIndex = []string{"https://a.test/", "https://b.test/"}, 1
	m.current = mustURL(t, "https://b.test/")

	// Back: the Cmd is built (sequence advances), then the load fails.
	if cmd := m.step(-1); cmd == nil {
		t.Fatal("no navigation command")
	}
	m.Update(pageLoadedMsg{seq: m.navSeq, hist: 0, err: errors.New("boom")})
	if m.hIndex != 1 || m.current.String() != "https://b.test/" {
		t.Fatalf("failed Back moved history: index %d current %s", m.hIndex, m.current)
	}
	// Back again, but a newer navigation (a link) starts before it lands.
	_ = m.step(-1)
	backSeq := m.navSeq
	_ = m.admit("https://c.test/")
	m.Update(pageLoadedMsg{seq: backSeq, hist: 0, html: "<p>a</p>", base: mustURL(t, "https://a.test/")})
	if m.hIndex != 1 || m.current.String() != "https://b.test/" {
		t.Fatalf("superseded Back landed: index %d current %s", m.hIndex, m.current)
	}
	m.Update(pageLoadedMsg{seq: m.navSeq, hist: pushNew, html: "<p>c</p>", base: mustURL(t, "https://c.test/")})
	if m.hIndex != 2 || len(m.history) != 3 || m.history[2] != "https://c.test/" {
		t.Fatalf("link did not push: %v @%d", m.history, m.hIndex)
	}
	// A successful Back lands and may record a redirected URL.
	_ = m.step(-1)
	m.Update(pageLoadedMsg{seq: m.navSeq, hist: 1, html: "<p>b</p>", base: mustURL(t, "https://www.b.test/")})
	if m.hIndex != 1 || m.history[1] != "https://www.b.test/" {
		t.Fatalf("Back did not land: %v @%d", m.history, m.hIndex)
	}
}

// The terminal size known before the first frame decides the first
// re-flow width; nothing stays at the 800 px default.
func TestFirstFrameReflowsAtTerminalWidth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chan tea.Msg, 256)
	l := loop.New(ctx, loop.Config{Width: 800, MinFrame: time.Millisecond}, chanSink(sink))
	go l.Run()
	m := New(l, netpolicy.Policy{}, NavFree, "")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	l.LoadPage(`<p>hello</p>`, "https://example.test/", nil)

	deadline := time.After(5 * time.Second)
	var first loop.FrameMsg
	for first.Result == nil {
		select {
		case <-deadline:
			t.Fatal("no first frame")
		case msg := <-sink:
			if f, ok := msg.(loop.FrameMsg); ok && f.Err == nil {
				first = f
			}
		}
	}
	if first.Result.Bounds().Dx() != 800 {
		t.Fatalf("first frame width %d", first.Result.Bounds().Dx())
	}
	m.Update(first) // creates the viewer at 200 columns and tells the loop the width
	for {
		select {
		case <-deadline:
			t.Fatal("no re-flow at the terminal width")
		case msg := <-sink:
			if f, ok := msg.(loop.FrameMsg); ok && f.Err == nil && f.Result.Bounds().Dx() == 2000 {
				return // 200 glyph-fallback columns × 10 px, nthtml's own rule
			}
		}
	}
}

type chanSink chan tea.Msg

func (c chanSink) Send(m tea.Msg) { c <- m }
```

## Appendix B. go.mod of the spike

```
module nimbleterminal.dev/ntbrowser

go 1.26.8

require (
	charm.land/bubbletea/v2 v2.1.0
	github.com/charmbracelet/x/ansi v0.11.9
	github.com/dop251/goja v0.0.0-20261024144015-2a9269b6b6de
	golang.org/x/net v0.61.0
	nimbleterminal.dev/nthtml v0.1.0
)

require (
	charm.land/bubbles/v2 v2.2.1 // indirect
	charm.land/lipgloss/v2 v2.0.6 // indirect
	github.com/NimbleMarkets/oksvg v0.1.2 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20261008173134-6b8d4baf91b4 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dlclark/regexp2/v2 v2.8.1 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.31 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef // indirect
	github.com/xo/terminfo v1.2.0 // indirect
	github.com/zkrebbekx/go-mermaid v0.1.6 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	nimbleterminal.dev/ntfonts v0.1.0 // indirect
	nimbleterminal.dev/ntpicture v0.1.1 // indirect
	nimbleterminal.dev/ntsvg v0.5.2 // indirect
)

// Same WASM-capable Bubble Tea pin as nthtml and ntvideo.
replace charm.land/bubbletea/v2 => github.com/neomantra/bubbletea/v2 v2.0.0-20261009201241-c397c32a5074
```
