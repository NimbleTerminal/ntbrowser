# ntbrowser: security profiles, content routing, click-to-play video — design

Date: 2026-10-31 · Status: approved 2026-10-31 (Evan); implemented on branch `profiles` · Author: Claude
Extends the architecture, subresources and forms designs in this directory. Everything there stands. Marked **Evan** where he decided; everything else is my call.

## 1. Intent

ntbrowser is a mini web sandbox. Today its security is a scatter of flags, and everything that is not an HTML page fails: a link to an `.mp4` ends in `load failed: content type "video/mp4"`. Evan hit this on nimbleterminal.com, whose recordings are poster images inside links to `.mp4` files.

This round makes two things explicit:

- **The sandbox's boundary** (**Evan**). There is a small, complete list of entrypoints into the underlying system, each with a knob. A named *profile* is a set of knob positions. The threat is not only a hostile page: it may be the thing inside the sandbox, including a misaligned agent trying to escape. Today this is for testing and browsing; eventually it is a sandbox for computer use.
- **What content is, apart from where it goes** (**Evan**). Incoming content is first understood abstractly, as a small tree. Only then does a surface decide how best to show each piece. In a terminal a video is Kitty placements, in a native app it is composited into a frame, and in a wasm browser it is a `<video>` element.

Click-to-play video is the first feature that needs both.

## 2. Goals, non-goals, anti-goals

**Goals**
- A complete, documented list of gates. Every way out of the sandbox is one of them.
- Named profiles, inspectable before launch, fixed for the life of the process, and not visible to the page.
- A content tree that navigation produces for every response, with one decision about what a response is, shared by all surfaces.
- A terminal presenter for pages, images, animations (GIF, WebP), video and unknown content.
- Opt-in click-to-play video, decoded in a separate sandboxed helper process that is the pattern for any later risky decoder (**Evan**).
- OS-enforced file and process limits where the platform allows, reported honestly where it does not.

**Non-goals (this round)**
- The native and wasm presenters. Their interface is defined and tested with a fake; they are not built.
- Inline video drawn in the page, autoplay, audio-only pages, captions, downloads.
- An agent API for computer use. The profile format is designed for it; the API is not built.
- Per-site profiles (IE's zones). One profile per process.
- POST forms.

**Anti-goals (a failure even if it works)**
- A profile that claims more than it enforces. If a gate's strength is lower than the profile asks for, the process refuses to start, or says so; it never silently degrades.
- IE's failure: levels nobody could read. Every profile prints as a short table of plain settings.
- Anything inside the sandbox changing, widening or reading the profile.
- A native decoder touching content the reader did not ask to play.
- Native code in the main process, ever.
- A second parser. For a page, the DOM already is the tree.

## 3. What recon established

- **Flags today** (`cmd/ntbrowser/main.go`): `-nav`, `-allow`, `-allow-private`, `-subresources`, `-external-scripts`, `-width`, `-slice`, `-seed`. Defaults: `-nav free`, `-subresources any`, inline scripts on.
- **Navigation** runs `netpolicy.Fetcher.Do` with `KindDocument`. `acceptableContentType` admits only text-like types, and the whole body is read under `MaxBody` (`--page-bytes`: 1 MiB for ntbrowser, 4 MiB for glynx, at most 8 MiB, since 2026-11-09; the same figure bounds `dom.Limits.TotalBytes` and nthtml's input cap is raised to match).
- **The only `os.Open`** in the program is the reader `internal/browsercmd` passes as `app.Options.OpenLocal`, for the `file:` start page. `jsvm` and `dom` import no system packages (enforced by test).
- **The script clock** is already coarsened (`jsvm/sandbox.go`), and there is no JIT.
- **ntpicture** plays GIF and animated WebP in pure Go (`animation`, `playback`).
- **ntvideo v0.3.0** is a Bubble Tea widget (`Init`, `Update`, `View`, `SetBounds`, `Play`, `Pause`, `Seek`, `Close`, `WithPoster`, `WithScrubber`). Native playback loads libmpv at runtime through goffi (cgo-free, but `unsafe`); without libmpv it shows "install mpv to play video". It sets `config=no`, `load-scripts=no`, `ytdl=no`, `sub-auto=no` and `protocol_whitelist=file`, so mpv opens only local files.
- **Constraint:** every build is `CGO_ENABLED=0`. macOS `sandbox_init` is reachable only through libSystem; Linux landlock and seccomp are plain syscalls.

## 4. Architecture

```
 launcher ──► Profile (validated, frozen) ──► every gate below reads it, nothing inside can write it

 URL ─► netpolicy (gate) ─► headers ─► content.Classify ─► content.Node ─► profile check ─► body by kind
                                                              │
                                                              ▼
                                       Surface.Present(node) ─► terminal: viewer / picture / player / status
                                                                native, wasm: interface only
```

New packages: `profile` (data, validation, printing; no system imports) and `content` (node types and classification; no system imports, builds for wasm). `sandbox` holds the OS enforcement, per platform, and is only imported by `cmd/ntbrowser`.

## 5. Decisions

### Gates and profiles

1. **The gates are this list; anything else does not exist** (the list is mine, the principle **Evan**). A later feature adding a way out adds a gate here first, and `AGENTS.md` says so.

   | Gate | Values | Covers |
   |---|---|---|
   | `script-navigation` | `off`, `origin`, `allowlist`, `confirm` | Location requests; intersection with navigate; off by default |
   | `script-navigation-limit` | 0/default 8 or 1–100 | document request count and automatic chain budget |
   | `navigate` | `off`, `allowlist`, `confirm`, `free` | top-level navigation. `off`: only the start URL given at launch is fetched, and nothing after it |
   | `subresources` | `off`, `origin`, `any` | stylesheets and images, including detached script-created `Image` requests |
   | `frame-dom` | `off`, `same-origin` | bounded synchronous DOM sharing between same-origin frame realms; off by default |
   | `frames` | `off`, `origin`, `allowlist`, `any` | isolated child document realms; off except in glynx |
   | `frame-grants` | source/destination origin pairs | frame document admission only; independent of fetch/scripts/navigation |
   | `fetch` | `off`, `origin`, `allowlist` | `fetch()` from page script |
   | `hosts` | list | fetch/navigation allowlist hosts; does not authorize scripts |
   | `script-sources` | `grants` (default), `any` | external script origin admission only; requires scripts=external |
   | `script-grants` | source/destination origin pairs | cross-origin script admission only |
   | `script-prompts` | off/on | reader may admit one script request or grant the origin pair for this session; default off |
   | `private-network` | `off`, `on` | loopback and private addresses |
   | `scripts` | `off`, `inline`, `external` | page script |
   | `devtools` | boolean, default false | host-only read-only inspection API and local panel; no remote listener or evaluation |
   | `diagnostics` | boolean, default false | host writes observations to one preopened private file; no page access |
   | `session-storage` | `off`, `on` | bounded per-origin memory store for one browser session; off by default |
   | `cookies` | `off`, `session` | bounded memory-only cookie store shared across navigation; off in locked/reader/browse |
   | `dynamic-code` | `off`, `on` | native direct/indirect eval and Function-family string compilation; off in locked/reader/browse |
   | `clock` | milliseconds | shared resolution for Date, performance and animation-frame timestamps (speculation timing) |
   | `files` | `off`, `start-page`, `temp` | `start-page` reads the one `file:` URL given at launch; `temp` adds the private media directory |
   | `native-code` | `off`, `on` | native code, which exists only in the media helper (decision 21); the main binary never loads any |
   | `media` | `off`, `image`, `animation`, `video` | the most a top-level response may be presented as (cumulative). Images inside a page are the `subresources` gate; the printed table says so |
   | `max-media-bytes` | bytes | download cap for media |
   | `clipboard` | `off`, `copy` | selected-text copy to the terminal clipboard |
   | `environment` | `off` | reserved: nothing in the sandbox reads the environment |
   | `process` | `off` | after startup the program starts no processes; there is no `on`. The media helper is started before lockdown (decision 22) |

   **Dynamic-code extension (2026-11-06).** The gate defaults off, including in
   `browse`. When off, eval and ordinary/async/generator function constructors
   throw `EvalError`, including prototype `.constructor` paths. When on, the
   engine's native intrinsics preserve lexical scope and execute inside the
   existing runtime, with its parser source-map protection, call-stack limit,
   watchdog and heap checks. They confer no network, file or navigation rights.
   String timers remain unsupported. The gate is checked from the host Profile;
   page code cannot enable it. Native dynamic compilation has no per-source size
   hook and cannot be interrupted mid-parse/compile in the pinned Goja version;
   the top-level 1 MiB script limit does not apply to generated strings. Time
   interrupts take effect when execution resumes, and memory checks remain soft.

   **Anchor interface extension (2026-11-06).** URL/attribute/token-list bindings
   are local DOM operations and create no new authority. Reader activation uses
   the final anchor URL after click listeners, then the existing `navigate` gate
   and `netpolicy` checks. `target`, `download`, `ping`, `rel` and `referrerPolicy`
   reflect data only: no additional browsing contexts, disk downloads, audit
   requests, credentials or referrers are enabled. Script `.click()` activation
   is not exposed. A future implementation of any such effect must explicitly
   define its gate here before adding the host action. The zero profile still
   denies navigation. See README for the operation-to-gate table.

2. **Closed by default** (**Evan**). The zero `Profile` is every gate `off`, and code reads the profile, not a flag. A profile only opens gates.
3. **Three built-in profiles:**

   | | `locked` | `reader` | `browse` |
   |---|---|---|---|
   | `navigate` | `off` | `confirm` | `free` |
   | `subresources` | `off` | `origin` | `any` |
   | `fetch` | `off` | `off` | `allowlist` |
   | `scripts` | `off` | `off` | `inline` |
   | `dynamic-code` | `off` | `off` | `off` |
   | `files` | `off` | `off` | `start-page` |
   | `clock` | n/a | n/a | today's value |
   | `native-code` | `off` | `off` | `off` |
   | `media` | `off` | `image` | `animation` |
   | `clipboard` | `off` | `copy` | `copy` |
   | enforcement | `required` | `best-effort` | `best-effort` |

   `locked` still fetches and shows the one start URL. OS policies cannot name hosts: they allow IP networking, and netpolicy is what confines hosts, so those gates are `checked`. `browse` is exactly today's behaviour and is the default, so nothing changes for an existing command line. Video is never in a built-in.
4. **Choosing a profile.** `-profile name` picks a built-in; `-profile-file path` reads a JSON file with the same fields plus an optional `base` built-in. Unknown fields are errors. Existing flags remain and apply on top, and new flags exist for the remaining gates. The result is the *effective profile*. The `-media video` flag is the one shorthand: it also sets `native-code=on` and `files=temp`, because a person typing it has asked for video, and `-show-profile` prints all three. A profile file gets no shorthand: it lists them or fails validation (decision 5).
5. **Validation fails at startup, never later.** Dependencies are checked: `media=video` requires `native-code=on` and `files=temp`. So is the start URL (`ValidateStart`): a `file:` start URL under `files=off` is an error, so `ntbrowser -profile locked file:///x.html` does not run. A violation is an error naming the gate, and the program does not run.
6. **`-show-profile` prints the effective profile as a table and exits**, including each gate's strength on this machine. The running app shows the profile's name in the help line.
7. **Frozen and private** (**Evan**). The profile is built before the loop starts and passed by value. No binding exposes it, and nothing in `jsvm` can reach it. A page sees only that things fail, as it does for an unreachable host.
8. **Each gate has a strength** (**Evan**), and the table reports the strongest that applies:
   - `compiled-out`: the code is not in this binary (build tag `nocjk`; native code is only ever in `ntbrowser-media`).
   - `os`: the kernel refuses (decision 10).
   - `checked`: our code refuses.
9. **`enforcement` is a profile field:** `required` or `best-effort`. Under `required`, if OS enforcement cannot be applied on this machine, the program exits with an error. Under `best-effort` it runs, and `-show-profile` and the status line on start say `checked` for those gates.
10. **OS enforcement, applied once at startup, irreversibly, before any page content is touched:**
    - **Linux:** landlock (filesystem: read the start page and the Go runtime's needs, read-write the media temp directory, nothing else) and seccomp (deny `execve`, `ptrace`, `mount` family, kernel-module and `bpf` calls). Pure syscalls, so cgo-free. Kernels without landlock are "unavailable".
    - **macOS:** `sandbox_init` needs cgo, so the process re-executes itself under `/usr/bin/sandbox-exec -p <generated policy>` at startup, guarded by an internal argument so it happens once. `sandbox-exec` is deprecated but present; if it is missing or cannot apply (inside another sandbox), enforcement is "unavailable". The re-exec carries the decided profile as data, so nothing is read again inside the sandbox, and a refused read proves the sandbox is really there. The re-exec is the only process ntbrowser starts, it happens before the sandbox exists, and `process=off` applies from then on.
    - **Other platforms and wasm:** "unavailable". In wasm the browser is the OS sandbox.
    - The main process's policy never allows native code or process creation. The helper has its own policy (decision 24).
11. **Speculation** (**Evan** raised it). No JIT and the `clock` gate are this round's measures. They make timing attacks harder; they do not make them impossible, and the doc says so: nothing in-process is a real boundary against a speculative read of the same address space. The media helper is the first use of a process boundary; a separate process per task is the strong boundary for computer use, later.

### Content

12. **The content tree** (**Evan**, shape mine). `content.Node` carries `Kind` (`Page`, `Image`, `Animation`, `Video`, `Text`, `Unknown`), `MIME`, `Size` (−1 unknown), `URL`, `Poster`, `Width`, `Height`, and `Children`. A page's DOM stays the DOM; a `Page` node refers to it and has a child for each media element a surface may present separately. Direct navigation to an `.mp4` is a `Video` node with no parent.
13. **Classification uses headers, before the body.** A new netpolicy `Fetcher.Open` returns status, final URL and headers with the body unread, under every existing check; `Do` is built on it. `content.Classify(mime, url)` decides:
    - `text/html`, `application/xhtml+xml` → `Page`
    - `image/gif`, `image/webp` → `Animation` (a still image once decoded if single-frame)
    - other decodable images → `Image`
    - `video/*` → `Video`
    - other `text/*`, JSON → `Text`
    - anything else, or an unreadable type → `Unknown`
    
    The server's type is believed; a missing type or `application/octet-stream` goes by the URL's extension for media only, never to `Page`. A `file:` start page has no type and is always `Page`, as today; local media files are not a feature of this round.
14. **The body is read by kind, after the profile check.** `Page` and `Text`: 1 MiB, as today. `Image`, `Animation`, `Video`: up to `max-media-bytes` (default 64 MiB), read to memory for images and animations and to the temp directory for video. A kind the profile does not allow reads nothing: the connection is closed and the status line says why. `Content-Length` over the cap refuses before reading; a body that runs over it is cut off and the file deleted.
15. **Unknown content is never interpreted.** The status line shows `video/x-foo · 362 KB · can't display`, and the current page stays. There is no download.
16. **Plain text** is shown as a page of preformatted text: the loop escapes it into a `<pre>` document, without scripts.
17. **Surfaces** (**Evan**). `content.Surface` is the interface a host implements:

    ```go
    type Surface interface {
        Capabilities() Capabilities              // what it can draw (Kitty, composited frames, <video>)
        Present(n *Node, body Body) Presentation // how it will show n
    }
    ```

    The profile is checked in the core before `Present`, so a gate applies the same way everywhere. A surface may choose a lesser presentation (a poster and a play prompt, or a text line), never a greater one.
18. **Terminal presenter (this round).** `Page`: as today. `Image`, `Animation`: a full-view picture through ntpicture (`animation.DecodeContext` with its limits, `playback` for motion), with Esc or Back to return. `Video`: the player (decision 21). `Text`: as a page. `Unknown`: the status line.
19. **Navigation to an image, animation or text** is a normal history entry; Back returns. A video is an overlay on the current page, not a history entry; Esc returns. With no page beneath (a video start URL), Esc quits.

### Video

20. **Only a reader action starts video** (**Evan**: opt-in, click-to-play). Activating a link that resolves to a video, or a focused `<video>` element, starts it. Script cannot: there is no `play()` binding, and `autoplay` is ignored. A `<video>` with a `src` or a `<source>` child becomes focusable only when `media=video`; the first `<source>` whose `type` classifies as `Video` wins. This is best-effort: nthtml gives `<video>` a box only with a known `width` or `height`, so one without has no box and cannot be reached, and nothing is synthesized for it. `poster` is not fetched this round. A poster image inside a link, the nimbleterminal.com case, is an ordinary `<img>` and works; that is the case this round must get right. With video off, activating one shows the status line `video is off in this profile`.
21. **Risky decoders run in a helper process** (**Evan**: the revised B, because it is a model for other media types). The main binary stays pure Go and static: it links neither ntvideo nor goffi, so OS enforcement holds on it in every profile. `cmd/ntbrowser-media` is a separate binary, in its own nested Go module so the main `go.mod` never gains ntvideo or goffi, holding the native code. The helper never touches the terminal. It produces pixels; the main process treats them as opaque bytes of a checked size and hands them to ntpicture, which picks the transport. So it works on any terminal, over SSH and in ntrecord, and a native surface can composite the same frames.
22. **The helper starts at launch, before the main process locks itself down, and only when `media=video`.** The main process then never needs permission to start anything, so `process=off` holds in every profile and no exec right appears in its policy. The helper idles, with libmpv unloaded, until the first play. It is one process for the session: if it dies, the player says `media helper stopped; restart ntbrowser` and the page goes on. (Starting one per video would need an exec right in the main policy, which is what this avoids. A compromised helper can reach only later videos' pixels.) `ntbrowser-media` is found next to the main executable; if it is missing, `media=video` fails validation.
23. **The helper protocol is generic, not video-shaped.** A stream socket pair created at launch carries framed binary messages: from the main process `open(name)`, `play`, `pause`, `seek`, `resize(w,h)`, `close`; from the helper `status(state, position, duration, error)` and `frame(seq, w, h, rgba)`. `kind` in the handshake names the decoder, so another media type is another decoder behind the same protocol. Frames are RGBA with `w*h*4` bytes, `w` and `h` no larger than requested, checked by the main process; a violation stops the helper. A pipe-style socket costs one copy per frame and needs no cgo; shared memory is a later optimisation behind the same protocol (ntpicture's shared memory needs cgo on macOS).
24. **The helper's sandbox is narrower than the main process's, and minimal** (my call; Evan did not choose yet). It has no network, no terminal, an empty environment, and reads only libmpv and its libraries plus the media temp directory. Software decoding, no GPU, no audio: the player says `no audio in this profile`. Terminal recordings are silent, so nothing is lost today, and sound or GPU can become gate values later. On Linux, goffi appears to stop a process restricting all its threads, so the helper does not restrict itself: the static `ntbrowser` binary, started in a hidden stage, applies landlock and seccomp and then `execve`s the helper, and the restrictions carry across. On macOS it starts under its own `sandbox-exec` policy. Because it is started before the main lockdown, it is not nested in the main policy.
25. **The player** is in `app`: a view filling the viewer's area, fed by helper frames through ntpicture, with a scrubber from `status`. Keys: Space pause, ←/→ seek 5 s, Esc back, Ctrl+C quit. A page keeps running beneath it, and its frames are not shown.
26. **Download first, then play.** The file comes through netpolicy, under the `navigate` gate, into a private directory created before lockdown (`os.MkdirTemp`, mode 0700), with a random name and no extension from the URL. The helper gets only that name, reads it from the directory its sandbox allows, and mpv sees a local file. Every network request stays under netpolicy. The status line shows progress. The file is deleted when the player closes and the directory at exit; a crash leaves it in the OS temp area (accepted).
27. **Needed from ntvideo:** today its frames come only through the Bubble Tea `Model` (the backend's `Frame` is unexported, and `WithFrameTransform` is a stopgap), and `hwdec=auto-copy` and audio are fixed. The helper needs a headless player: load a local path, play, pause, seek, pull RGBA frames at a requested size, status, with hardware decoding and audio off. That is a small upstream request, and the plan's first dependency.

## 6. Interfaces

```go
// profile: plain data, no system imports.
type Profile struct {
	Name string
	Navigate, Subresources, Fetch, Scripts, Files, NativeCode, Media, Clipboard string // typed enums in code
	Hosts          []string
	PrivateNetwork bool
	ClockMillis    int
	MaxMediaBytes  int64
	Enforcement    string // "required", "best-effort"
}
func Builtin(name string) (Profile, bool)
func Parse(jsonBytes []byte) (Profile, error) // unknown fields are errors; base resolved
func (p Profile) Validate() error // also: helper binary present when media=video
func (p Profile) ValidateStart(rawURL string) error
func (p Profile) Table(s Strengths) string

// netpolicy
func (f *Fetcher) Open(ctx context.Context, r Request) (*OpenResponse, error) // headers checked, body unread
// KindNavigate admits any content type; the caller classifies.

// content: no system imports, builds for wasm.
func Classify(mime, rawURL string) Kind
type Node struct { /* decision 12 */ }

// sandbox: per platform, imported only by cmd/ntbrowser and cmd/ntbrowser-media.
func Apply(p profile.Profile, start string) (Strengths, error)

// mediaproc: the helper protocol (no system imports beyond the socket it is given).
type Client struct{ /* main side: Open, Play, Pause, Seek, Resize, Close, Frames, Status */ }
type Decoder interface{ /* helper side: one per kind */ }
```

`app.New` and `loop.Config` take the `Profile` in place of the flags they take today; the subresource and fetch policies are derived from it.

## 7. Threat model additions

- **A hostile page or agent inside the sandbox:** it has only the JS surface; the gates are not reachable from it, and OS enforcement backs the file and process gates.
- **Hostile media:** images and animations go through pure-Go decoders with limits. Video reaches C decoders only after a reader action in a profile that opened `native-code`, and only in the helper, whose sandbox has no network, terminal or environment. A compromised decoder can send wrong pixels, which are bounded and checked, or stop. This is the main risk the round adds, and it is why video is in no built-in.
- **Helper channel:** the main process parses a small fixed binary protocol from a process it must treat as hostile; the parser is fuzzed, and sizes are checked before allocation.
- **Type confusion:** a server calling HTML `video/mp4` gets it treated as video; a server calling a video `text/html` gets it parsed as a page, safely. mpv probes files itself, so a non-video in a video response fails in mpv, in the same sandbox.
- **Temp files:** private directory, random names, deleted on close. `files=temp` adds nothing the page can read: no binding touches the filesystem.
- **Honest reporting:** a gate reported stronger than it is would be a security bug, so `Strengths` comes from what `Apply` actually achieved.

## 8. Verification the plan must include

- `profile`: built-ins match the table; the zero value is all closed; unknown JSON fields fail; dependency violations fail with the gate's name; `browse` produces exactly today's fetcher, subresource and script settings (a test against the current defaults).
- Flags override, and `-show-profile` prints the effective result.
- `content.Classify` table, including missing and lying types, and no path from octet-stream to `Page`.
- `netpolicy.Open` keeps every existing check (redirect, address, cookie, scheme tests run through it). Over-cap by `Content-Length` and by actual bytes.
- Each kind × each `media` value: allowed presents; refused reads no body and leaves the page in place.
- Each gate closed produces the refusal it should, end to end in `loop`/`app`: `scripts=off` runs no script, `fetch=off` rejects, `navigate=off` refuses a link, `clipboard=off` does not copy.
- `sandbox`, Linux (CI or a container): under `locked`, a child test process cannot read `/etc/passwd`, write outside temp or `execve`. macOS: the same through the re-exec. `required` with enforcement unavailable exits non-zero.
- Video: no reader action, no player; script cannot start it; `autoplay` ignored; a missing helper binary fails validation; temp file gone after Esc; no helper process exists when `media` is below `video`.
- Helper: the main binary links no goffi or ntvideo (`go list -deps` test); the protocol parser fuzzed; oversize frames, bad sizes and a killed helper leave the page running; helper sandbox tests (no network, cannot read `/etc/passwd`, no terminal, empty environment); a fake `Decoder` drives the player without mpv.
- A fake `Surface` proves a presenter can choose a lesser presentation, and that a refused node never reaches it.
- Live: nimbleterminal.com, activating a recording, plays under `-media video`, and shows the refusal line under `browse`.
- `GOOS=js GOARCH=wasm` builds `profile` and `content` along with the existing list.

## 9. Left to the builder

The exact seccomp list, the generated macOS policy text, Go type names for gate values, wording of status lines, whether `Text` wraps at the terminal width, and how the progress line looks.

## 10. Shared worker integration (2026-11-05)

OS worker launch and policy construction use `ntconfine` directly. The helper
continues to start before lockdown and the macOS re-exec still carries its socket.
`ntmediabox.NewClientWithKill` owns connection termination, including failed
handshakes, EOF, protocol errors, and explicit close; the kill callback runs once.
Frames and statuses retain their command generation until the UI consumes them,
so queued results from before a seek, stop, or replacement cannot update a new
playback state.

`ntbrowser-media` uses `ntvideo.NewWorkerDecoder` instead of a private Player
adapter. Open requests carry the profile's media-byte limit; the shared decoder
also bounds source dimensions using ntvideo's defaults and releases the active
media on Stop. Software decoding, disabled audio, reader-initiated playback,
and the existing profile gates remain in force. No render-protocol or caption
controls are exposed by ntbrowser in this integration.

## 11. ntmediabox usage audit (2026-11-05)

The streaming worker is the correct boundary for native video. A rendering
worker is also appropriate for hostile pure-Go media: memory safety and input
budgets do not make a synchronous decoder forcibly cancellable. The current
implementation does **not** isolate all media rendering in subprocesses.

| Path | Current execution | Appropriate worker boundary |
| --- | --- | --- |
| Click-to-play video | `ntbrowser-media`, streaming Client/Serve, native code only in the nested module | Keep the streaming protocol and startup-only launch |
| Top-level still images | `app.decodeStill`, bounded in-process decoding | RenderClient; ntpicture's decoder worker accepts a preconnected client |
| Fetched/data-URL images | `subres.decodeFetched` / `decodeDataURL`, bounded in-process decoding | Same still-image service; fetch and URL policy stay in the host |
| Inline, fetched, and data-URL SVG; Mermaid | nthtml's DefaultDiagramRenderer / ntsvg in the host | RenderClient adapter covering both Mermaid conversion and SVG rasterization |
| CSS data-URL backgrounds | nthtml decodes internally during layout | Must route through the worker too; changing only subres would leave this path local |
| GIF/WebP animation | ntpicture animation player in-process, frame and pixel budgets | A persistent streaming decoder, or a separately designed stateful render session; a still-image worker would lose animation |
| HTML/CSS layout and DOM/JS | Budgeted document rendering and interpreter in-process | Not interchangeable with nttypeset's worker: ntbrowser needs nthtml boxes, anchors, links, and mutable DOM correspondence |
| Fonts | Embedded trusted font sources | No extra process solely for these sources; untrusted web fonts would require a new policy and audit |
| PDF and other unknown responses | Refused without decoding | No worker needed until support is explicitly added |
| Kitty encoding and terminal drawing | Host, using checked decoded pixels | Keep in the host; workers must never gain terminal access |

The existing video integration now separates UI delivery from the protocol reader
with bounded, coalesced notifications. A blocked Bubble Tea Send cannot prevent
EOF/protocol-error handling. Startup retains Helper.Close for termination and
reaping, including error exits. After macOS re-exec, the inherited socket is
converted back to a deadline-capable connection and cleanup kills and waits for
the inherited child. No helper is spawned after lockdown.

A broader rendering-worker implementation needs a dedicated pure-Go helper,
started before lockdown independently of `media=video` (inline page images and
diagrams are not governed by the top-level media gate). It must not reuse the
native helper for profiles with native-code off. Keep all network fetching and
file admission in the host, pass bytes with explicit request/pixel/deadline
limits, and retain existing subresource and page budgets. Cover all decode entry
points above, with no silent local retry after worker failure. A timeout kills
the session; with `process=off`, automatic respawn is not allowed. Define either
a bounded pool created at startup or session-long degradation before enabling
this boundary. Report actual worker enforcement independently of protocol checks.

The render extension is a separate implementation decision; this audit does not
claim that pure-Go image, animation, diagram, or HTML rendering is already
process-isolated.

The cross-project [security landscape](2026-11-05-nimbleterminal-security-landscape.md)
and [boundary review](2026-11-05-security-landscape-review.md) distinguish
existing library guarantees from the application integration work described above.


## Diagnostic capture extension (2026-11-06)

`--diagnostics FILE` opens the diagnostics gate, creates a new 0600 file with
O_EXCL before confinement, and retains only that descriptor. A JSON profile can
set `diagnostics: true`, but a CLI destination is still required. macOS carries
the descriptor across re-exec and permits file-write-data only at the resolved
literal path. Linux retains the preopened descriptor without opening directory
write access. The page cannot select a path, enable capture, inspect the file,
or submit debugger commands. Gate-off runs create no capture.

The host emits versioned JSONL through a bounded nonblocking queue. The stream
is capped at 32 MiB; each record is capped at 4 MiB, the queue at eight records.
It includes original HTML and render snapshots, profile/enforcement, script
results, console/errors and network metadata/refusals. Snapshot serialization
reuses the existing render pass on the VM goroutine; capture never touches the
VM or mutable DOM from an observer goroutine. Dropped counts and byte-limit
termination are explicit. No cookie or authorization headers or request bodies
are collected; URLs and page contents can still be sensitive. Output is
untrusted data, not instructions for an agent reading it.

This is the first inspection transport, not a DevTools or MCP implementation.
It deliberately has no incoming command channel, page mutation, eval, or
permission override. A future live inspection protocol must preserve these
boundaries and separately gate any write/evaluation operations.


## Session cookie extension (2026-11-06)

This explicitly revises the architecture's anonymous-only rule when the
`cookies=session` profile gate is selected. The zero value and every built-in
stay off. The host creates one bounded memory-only store per browser launch and
shares it between navigation, page fetchers and subresources. The loop binds
cookie callbacks to the actual page URL, clearing any low-level VM overrides;
page code cannot choose another URL or access the store object.

`document.cookie` is an accessor even when off (empty reads, ignored writes).
The store follows RFC 6265 domain/path/expiry semantics, validates domains with
the public suffix list, protects HttpOnly from script reads/writes/deletions,
rejects insecure Secure cookies and secure-cookie shadowing, and enforces
__Secure-/__Host-/__Http- prefix constraints. SameSite is schemeful, defaults to
Lax, and None requires Secure. Cookies marked Partitioned are refused rather
than silently becoming unpartitioned. Navigation applies SameSite across the
initiator and redirect chain. Subrequests are stricter: only the document's
exact origin may send or receive cookies; cross-origin credentials include
is not supported. Fetch credentials omit disables both directions through
redirects. Network policy admission and header filtering remain in place.

Limits: 4096-byte Set-Cookie inputs, 50 entries per domain, 256 per session,
400-day maximum expiry, expired entries pruned before capacity checks; reject
new entries at capacity. No persistence, importing, external cookie access,
or new filesystem permissions. Diagnostics do not record cookie headers or
store contents (page-generated console text can of course include JS-visible
values). The source specifications are [RFC 6265 §5](https://httpwg.org/specs/rfc6265.html#user-agent-requirements) and [HTML document.cookie](https://html.spec.whatwg.org/multipage/dom.html#dom-document-cookie);
this remains a documented subset rather than full browser cookie conformance.


## Location extension (2026-11-06)

This revises the architecture's reader-only navigation assumption specifically
for Location. `script-navigation` is independent of and intersects `navigate`.
Off is the zero value and all built-ins. Origin forbids cross-origin targets
and redirects even if hosts are allowlisted; allowlist permits the current
origin plus configured hosts; confirm requires a reader decision for every
request. The existing navigation gate remains authoritative: off denies,
allowlist never becomes free after confirmation, confirm requires a prompt
before crossing origin. All requests use the vetted network path; file/data/
javascript URLs and embedded credentials are refused before host dispatch.

Bindings expose only plain-string navigation callbacks, bound by the loop to
the current page/generation. The host checks generation, source URL, and whether
a newer navigation is already in flight. Requests are coalesced per VM turn;
per-document emissions and automatic chains are capped (default eight, max 100).
Reader navigation/confirmation resets the chain budget. Pending prompts cannot
be replaced by scripts. Denials and requests are captured without opening any
new diagnostic capability. URL input length is bounded at 2048 bytes.

Replace/reload reuse the current history slot on successful load. Fragment
changes commit on the owning VM goroutine without refetching. These features
do not implement the full History platform or navigation events, ancestorOrigins,
frames, or cross-window Location semantics. (2026-11-08: `window.history` exists
as inert page-local state so scripts that wrap `pushState` run; it records
`state`, ignores URLs, keeps `length` 1 and never navigates.) Source: [HTML Location interface](https://html.spec.whatwg.org/multipage/nav-history-apis.html#the-location-interface).


## Animation resource controls (2026-11-06)

`frame-ms`, `frame-callbacks`, and `frame-budget-ms` tune scheduling and resource
limits without opening a new host capability. Defaults are 33 ms, 256 pending
callbacks, and 8 ms total callback execution per batch. Explicit values are
bounded to 1–1000 ms, 1–1024 callbacks, and 1–50 ms respectively; zero selects
defaults. Scripts remain controlled by the scripts gate and callback execution
retains the ordinary watchdog, strike and heap protections. Deferred callbacks
preserve order and cancellation across batch boundaries.


## Session storage extension (2026-11-06)

`session-storage=on` explicitly enables a loop-owned, memory-only Session.
The loop binds a Storage area to the current document's WHATWG-parsed HTTP(S)
origin and overrides any lower-level Sandbox capability. Scheme/host/effective
port isolate areas; opaque/file origins are refused. Hostile page strings
never become paths or request destinations. No netpolicy or OS sandbox grant
is added. Navigation retains the session store but replaces all JS wrappers;
loop shutdown clears it. Independent browser loops have independent stores.

`session-storage-bytes` defaults to 1 MiB per origin (maximum 8 MiB), and
`session-storage-total-bytes` defaults to 8 MiB (maximum 64 MiB). Zero selects
defaults. Keys plus values are charged by UTF-16 byte length. Fixed limits are
64 nonempty origins and 1024 keys per origin, including empty-value entries.
A rejected update is atomic; deletes/clear release quota. No eviction or disk
spill occurs. The closed gate/unsupported origin raises SecurityError on
sessionStorage access; failed writes raise QuotaExceededError. Errors use
standard names without adding a general DOMException implementation.

The bridge provides the Storage methods, length, named property operations,
and enumeration using Goja's explicit DynamicObject API, never reflected Go
structs. Stored data consists only of encoded strings, not runtime values.
Prototype members take precedence over same-named stored keys. Method calls
preserve UTF-16 code units, including lone surrogates; named-property keys have
Goja's string-key conversion limits. Accessor/symbol own-property descriptors,
StorageEvents, frames/opener cloning and localStorage are outside this scope.
No contents are emitted through diagnostics by the host. Source:
[HTML Web Storage](https://html.spec.whatwg.org/multipage/webstorage.html).

### matchMedia (2026-11-09)

`window.matchMedia` reads `jsvm.Config.Media`, a host closure returning the
loop's layout width, viewport height and preferred color scheme. These are
already observable through layout, so no gate is involved and no authority is
conferred; the loop calls `Sandbox.MediaChanged` after a width or scheme
change and the resulting `change` events run as one watchdog entry. Lists are
bounded (256 per page) and weakly held.

### Local storage and persistence extension (2026-11-08)

Two gates. `local-storage` (`off`, `on`; on only in glynx) binds
`window.localStorage` to a second `sessionstorage.Session` with the session
quotas; off leaves it absent (SecurityError), as closed session storage.
`persist` (`off`, `file`; file only in glynx) decides whether the cookie
store and the local store outlive the run. Evan's direction: glynx is a daily
driver and should keep its logins and site settings; ntbrowser stays
memory-only; one tighter switch, `--safe`, forces `persist=off`.

Persistence is one owner-only file per store under the state directory
(`$XDG_DATA_HOME/<command>`, 0700): `cookies.dat` and `local-storage.dat`.
They follow the diagnostics-file rules: opened before lockdown
(`O_RDWR|O_CREATE|O_NOFOLLOW`, 0600, regular file required), close-on-exec
cleared for the macOS re-exec, paths resolved and granted as literals for
`file-write-data`; the sandboxed process receives descriptors through
`state.StorageFD`/`state.CookieFD` and never opens a path. Each file is a
header line with the payload length followed by the store's JSON snapshot;
rewrites happen in place from offset 0, so no truncation is needed and stale
bytes are ignored. `Restore` re-applies every entry through the store's own
write path, so quotas, caps, origin parsing, expiry and the 400-day ceiling
hold for loaded data; oversize, unversioned or malformed files start a store
empty and are overwritten by the next write.

Only cookies with an expiry are written; session cookies end with the run,
as in a browser. The cookie store notifies a locked persister after each
stored write (fetch workers and the VM goroutine both write), which coalesces
to one snapshot a second and writes once more at Close, after the program
has stopped. The local store's change hook marks the loop dirty; a
one-second timer posts one `Snapshot` to the host sink and `Run`'s exit
flushes once more, and the host now waits on `Loop.Done` before returning
so that flush lands. A failing sink is reported once. Child frames get
neither store. Contents never enter diagnostics.

### Dynamic classic-script extension (2026-11-06)

DOM insertion adds no network authority: external dynamic scripts require the
existing `scripts=external` gate and use a script-specific policy derived from
the page-bound `netpolicy.Fetcher`. Shared fetch/navigation host lists confer no
script authority; exact source/destination origin grants control external access. `KindScript` additionally enforces 2xx and a strict supported
JavaScript MIME type. Redirect, private-address, cookie, timeout and body caps
remain in netpolicy. SRI-bearing requests use the verification rules below. Inline dynamic source requires both scripts enabled and dynamic-code
on, so DOM insertion cannot bypass the string-compilation gate.

The VM snapshots a script's URL and async mode at preparation and pins its
wrapper until completion. Started state outlives wrapper GC while its DOM node
exists. At most 32 external scripts may be pending, including completed responses
waiting for ordered predecessors; at most 256 dynamic elements prepare per
page. Failed admission still consumes the total budget. Caps throw TypeError;
network/policy errors dispatch element error events. The pending slot is released
after event delivery, and old-page completions are dropped by both page identity
and navigation generation. Workers carry plain request/response data only.

Inline dynamic execution reuses the active watchdog, never clears its interrupt
or rearms its budget. External execution and events use normal sandbox entries.
All sources pass the existing size limit, hardened Parse/CompileAST path and
heap checks; source-map loading stays disabled. DOM serialization omits scripts.

The lifecycle follows the classic-script portion of the
[HTML script processing model](https://html.spec.whatwg.org/multipage/scripting.html#prepare-the-script-element),
with the additional gates and limits above. Modules, import maps and CSP
are not implemented. Dynamic scripts are async by default;
async=false scripts execute in preparation order. Tests use local HTTP servers
with controlled completion order, with no public-network dependency.

### Script integrity extension (2026-11-06)

`netpolicy.Fetcher.Script` is the common entry for parsed and inserted external
scripts. It validates bounded integrity metadata, invokes the existing vetted
transport with KindScript, then hashes the decoded response body before returning
source. SHA-512 takes precedence over SHA-384 over SHA-256; equal-strength digests
are alternatives. Empty metadata preserves ordinary script loading. Unknown-only
or malformed recognized metadata, options, credentialed integrity requests, and
origin-changing redirects fail closed. At most 4096 metadata bytes and 32 tokens
are accepted. No dependency or profile permission is added.

For integrity loads across origins, the element must request anonymous CORS and
the response must carry a single ACAO value equal to the document's serialized
origin or `*`. The host supplies Origin. Same-origin redirects re-enter normal
policy; cross-origin resource redirect responses also undergo CORS checks.
Cross-origin cookies remain excluded by the cookie transport. The CORS state is
request-local context, never a shared mutable policy flag. Denials return no
source to the VM, and stale navigation checks still apply after verification.
This is a conservative subset of SRI, not general fetch CORS or CSP support.

### Source-scoped script grant extension (2026-11-06)

Script admission is independent of fetch admission. The loop derives an immutable
policy from the page transport, clears AnyOrigin and shared hosts, adds only
exact destinations granted to that page origin, retains cookies/IP/redirect/body/
timeout checks, and shares the original request semaphore. Per-request derived
transports close idle connections after completion. Same-origin requests still
require scripts=external; zero transport policies remain closed.

Saved grants are profile `script-grants` entries or repeatable --allow-script
arguments. Profiles validate at most 128 exact HTTP(S) origin pairs. With
script-prompts enabled, otherwise grantable requests can await a reader choice.
The UI offers once/session/deny and never persists grants itself. Session grants
are loop-owned and bounded to 128 pairs; pending prompts are bounded to eight
and expire after 30 seconds. IDs and document generations bind every decision;
page replacement stops timers and discards pending callbacks. Existing network
policy is checked with a candidate origin before showing a prompt and rechecked
on the actual request. A prompt cannot grant private-network or mixed-content
exceptions. Runtime DNS admission remains authoritative after approval.

The UI defers to active reader navigation/edit/search, prevents scripts replacing
an active permission prompt, blocks page mouse dispatch while prompting, uses
Alt-modified approval keys, and requires both origins to fit visibly. Denials
record source origin, requested URL, purpose and reason. No profile-writing or
new filesystem capability is introduced; persistent grants require explicit
profile edits outside the sandboxed browser.

## Isolated frame extension (2026-11-07)

Frames are opt-in independent document realms, including same-origin embeds:
parent/child DOM objects never cross a realm boundary. Frame references expose
bounded postMessage and window identity; direct document access is denied
unless the separate frame-dom gate below admits it.
Frame navigation requires the frame source/destination policy, and script
navigation additionally requires script-navigation=origin and navigate=free;
confirmation/allowlist modes are not approximated by automatic approval. No
child can navigate the top-level page. Frame loads retain SSRF, redirect,
MIME, mixed-content and response-size checks. Cookies and storage are disabled
in child realms in this first isolated mode; no disk or process authority is
added. Document/frame removal cancels descendants and stale messages/paints.
Hard ceilings bound frame count, nesting, raster area, document bytes, and
message queues across the frame tree. Unsupported sandbox capabilities,
transfers and DOM accesses outside the explicit shared subset fail closed.


The initial wire format is JSON-compatible values only, bounded by the existing
clone budgets and a tree-wide queue of 64 messages/4 MiB. Four child documents
and two nesting levels may be live; 64 total loads bound churn per root page.
Children have at most 2,000 nodes, 256 KiB document bytes, and 1024×768 raster
pixels. Srcdoc has a 64 KiB cap. Source/destination frame grants are capped at
128 entries and do not grant script or fetch access. Same-origin is implicitly
admitted in origin and allowlist modes. Opaque children cannot fetch or embed
descendants. Sandbox tokens other than allow-scripts/allow-same-origin fail
closed, except allow-forms, allow-popups and allow-modals, which are accepted
as inert (2026-11-08): they grant capabilities child frames do not have, and
refusing them dropped GitHub's password-reset frame. Child input is generation-checked, but native child form editing and
link/form navigation are not implemented. Renderer substitution preserves
class/inline style, not iframe tag selectors. Srcdoc uses the parent URL for
resolution and Location reporting in this subset.


### Shared DOM and frame scrolling revision (2026-11-07)

`frame-dom=same-origin` admits a bounded DOM bridge only when both realms opt in
and their canonical non-opaque HTTP(S) origins match. Frame admission grants
do not change this rule. Each operation validates the source and destination
revisions and their live ancestor iframe elements, including src/srcdoc/sandbox
attributes. Detachment or replacement revokes cached handles synchronously.
No document.domain relaxation is available. Child cookies/storage remain off.

A frame tree shares an execution mutex. Each Loop still enters its own Goja
runtime; foreign operations touch only plain DOM data and internal wrapper
lifetime bookkeeping under that mutex, never target JS callbacks. This avoids
blocking RPC cycles and runtime reentry. Render workers still receive immutable
snapshots; network workers and the UI never acquire this ownership lock.

Foreign handles are capped at 256 per calling realm and retained until that
realm closes. Pins participate in detached-node GC checks and are released on
caller shutdown. Inputs are capped before string export at 64 KiB (attribute
keys at 1 KiB); query/child snapshots contain at most 256 nodes. Existing target
DOM budgets apply atomically. Classic script creation/editing/executable moves
are denied; fragment scripts stay inert. Resource and frame effects keep their
existing target gates. Cross-document adoption, foreign listener callbacks and
arbitrary window globals are unsupported. See README for the precise DOM subset.

Child scrolling retains a 1024×768 raster ceiling and 16,384-pixel layout cap.
The paint carries its document origin and generation to parent presentation and
hit-testing. Wheel/keyboard gestures are contained in the focused child;
stale input cannot target replacement content. Scrolling creates no new host
authority. It uses the existing coalesced render cadence and watchdog budgets.


### Glynx browsing policy (2026-11-07)

The glynx executable defaults to the glynx profile. The ntbrowser executable
continues to default to browse; locked, reader and browse retain their existing
permissions. Both use the same runtime and lockdown startup.

Glynx enables scripts=external, script-sources=any, dynamic-code=on,
subresources=any, frames=any, frame-dom=same-origin, cookies=session,
session-storage=on, navigate=free and script-navigation=confirm. Fetch remains
origin-only; script/frame admission never grants fetch response access. The new
script-sources=any and frames=any modes admit public HTTP(S) destinations and
redirects only for their respective request purpose. They retain DNS/address
vetting, credentials restrictions, HTTPS downgrade rejection, MIME/SRI checks,
body/time limits, and the shared network semaphore. Private network access is
still off. Invalid/missing base origins fail closed. Explicit grants retain
their source/destination semantics when script-sources=grants or frames=allowlist.

Same-origin DOM isolation, iframe sandbox restrictions, child budgets, disabled
child cookies/storage, VM limits, and host filesystem/process confinement remain
unchanged. No persistence is added. --profile and --profile-file replace the
application default rather than merging glynx permissions into a restricted
profile. All gate flags still override the selected profile explicitly.

### Read-only devtools (2026-11-07)

The devtools gate is independent of diagnostics and closed in every built-in.
It authorizes host inspection only: capabilities, bounded frame/runtime/policy
snapshots, paginated DOM snapshots, and cursor-based recent event retrieval.
It does not authorize evaluation, DOM edits, page controls, breakpoints, or
remote connections. --devtools opens the local panel with F12; -d remains the
short form for the separate --diagnostics file capture.

The versioned devtools.Session API owns authorization, session identity, request
limits, a bounded event ring and capability discovery. Loop inspection posts to
the owner queue and returns copied data under the frame-tree owner lock; it never
enters Goja. DOM requests require a session, frame and unique document revision.
Pending navigation and detached/replaced frames invalidate inspection handles.
No page binding exposes the session. A future remote adapter must authenticate
before exposing this same API; a session ID is correlation, not a credential.

At most eight requests may be outstanding, with two-second deadlines, 50 items
per page, 256 events and 4096 bytes per event. Event polling reports overwritten
history. DOM output omits form values, script/style/textarea text, arbitrary
attributes, and URL credentials/query/fragment data. Network events omit headers,
bodies and raw error strings that could contain credentials. Console messages
and visible DOM text can still contain page data. No storage/cookie inspection
or implicit getter evaluation is provided. Existing JSONL capture retains its
separate opt-in full diagnostic semantics.
