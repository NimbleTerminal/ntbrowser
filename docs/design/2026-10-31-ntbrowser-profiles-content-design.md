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
- **Navigation** runs `netpolicy.Fetcher.Do` with `KindDocument`. `acceptableContentType` admits only text-like types, and the whole body is read under `MaxBody` (1 MiB for navigation).
- **The only `os.Open`** in the program is `cmd/ntbrowser`'s `app.SetLocalReader`, for the `file:` start page. `jsvm` and `dom` import no system packages (enforced by test).
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
   | `navigate` | `off`, `allowlist`, `confirm`, `free` | top-level navigation. `off`: only the start URL given at launch is fetched, and nothing after it |
   | `subresources` | `off`, `origin`, `any` | stylesheets, images |
   | `fetch` | `off`, `origin`, `allowlist` | `fetch()` from page script |
   | `hosts` | list | what `allowlist` means |
   | `private-network` | `off`, `on` | loopback and private addresses |
   | `scripts` | `off`, `inline`, `external` | page script |
   | `clock` | milliseconds | script clock resolution (speculation timing) |
   | `files` | `off`, `start-page`, `temp` | `start-page` reads the one `file:` URL given at launch; `temp` adds the private media directory |
   | `native-code` | `off`, `on` | native code, which exists only in the media helper (decision 21); the main binary never loads any |
   | `media` | `off`, `image`, `animation`, `video` | the most a top-level response may be presented as (cumulative). Images inside a page are the `subresources` gate; the printed table says so |
   | `max-media-bytes` | bytes | download cap for media |
   | `clipboard` | `off`, `copy` | selected-text copy to the terminal clipboard |
   | `environment` | `off` | reserved: nothing in the sandbox reads the environment |
   | `process` | `off` | after startup the program starts no processes; there is no `on`. The media helper is started before lockdown (decision 22) |

2. **Closed by default** (**Evan**). The zero `Profile` is every gate `off`, and code reads the profile, not a flag. A profile only opens gates.
3. **Three built-in profiles:**

   | | `locked` | `reader` | `browse` |
   |---|---|---|---|
   | `navigate` | `off` | `confirm` | `free` |
   | `subresources` | `off` | `origin` | `any` |
   | `fetch` | `off` | `off` | `allowlist` |
   | `scripts` | `off` | `off` | `inline` |
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
