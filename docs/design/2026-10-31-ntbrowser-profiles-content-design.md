# ntbrowser: security profiles, content routing, click-to-play video — design

Date: 2026-10-31 · Status: draft v1, awaiting approval · Author: Claude
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
- Opt-in click-to-play video through ntvideo.
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
   | `navigate` | `off`, `allowlist`, `confirm`, `free` | top-level navigation (`off`: the start page only) |
   | `subresources` | `off`, `origin`, `any` | stylesheets, images |
   | `fetch` | `off`, `origin`, `allowlist` | `fetch()` from page script |
   | `hosts` | list | what `allowlist` means |
   | `private-network` | `off`, `on` | loopback and private addresses |
   | `scripts` | `off`, `inline`, `external` | page script |
   | `clock` | milliseconds | script clock resolution (speculation timing) |
   | `files` | `off`, `start-page`, `temp` | `start-page` reads the one `file:` URL given at launch; `temp` adds the private media directory |
   | `native-code` | `off`, `on` | any native code loaded at runtime (today: libmpv) |
   | `media` | `off`, `image`, `animation`, `video` | the most a response may be presented as (cumulative) |
   | `max-media-bytes` | bytes | download cap for media |
   | `clipboard` | `off`, `copy` | selected-text copy to the terminal clipboard |
   | `environment` | `off` | reserved: nothing in the sandbox reads the environment |
   | `process` | `off` | reserved: the program starts no processes; there is no `on` |

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

   `locked` still shows the start page. `browse` is exactly today's behaviour and is the default, so nothing changes for an existing command line. Video is never in a built-in: it takes `-media video` or a profile file, because it opens `native-code` and `files=temp`.
4. **Choosing a profile.** `-profile name` picks a built-in; `-profile-file path` reads a JSON file with the same fields plus an optional `base` built-in. Unknown fields are errors. Existing flags remain and apply on top, and new flags exist for the remaining gates. The result is the *effective profile*.
5. **Validation fails at startup, never later.** Dependencies are checked: `media=video` requires `native-code=on` and `files=temp`. A violation is an error naming the gate, and the program does not run.
6. **`-show-profile` prints the effective profile as a table and exits**, including each gate's strength on this machine. The running app shows the profile's name in the help line.
7. **Frozen and private** (**Evan**). The profile is built before the loop starts and passed by value. No binding exposes it, and nothing in `jsvm` can reach it. A page sees only that things fail, as it does for an unreachable host.
8. **Each gate has a strength** (**Evan**), and the table reports the strongest that applies:
   - `compiled-out`: the code is not in this binary (build tags `nocjk`, `nomedia`).
   - `os`: the kernel refuses (decision 10).
   - `checked`: our code refuses.
9. **`enforcement` is a profile field:** `required` or `best-effort`. Under `required`, if OS enforcement cannot be applied on this machine, the program exits with an error. Under `best-effort` it runs, and `-show-profile` and the status line on start say `checked` for those gates.
10. **OS enforcement, applied once at startup, irreversibly, before any page content is touched:**
    - **Linux:** landlock (filesystem: read the start page and the Go runtime's needs, read-write the media temp directory, nothing else) and seccomp (deny `execve`, `ptrace`, `mount` family, kernel-module and `bpf` calls). Pure syscalls, so cgo-free. Kernels without landlock are "unavailable".
    - **macOS:** `sandbox_init` needs cgo, so the process re-executes itself under `/usr/bin/sandbox-exec -p <generated policy>` at startup, guarded by an internal argument so it happens once. `sandbox-exec` is deprecated but present; if it is missing, enforcement is "unavailable". The re-exec is the only process ntbrowser starts, it happens before the sandbox exists, and `process=off` applies from then on.
    - **Other platforms and wasm:** "unavailable". In wasm the browser is the OS sandbox.
    - The sandbox covers libmpv too, since it runs in our process. Generated policies allow the libmpv path only when `native-code=on`.
11. **Speculation** (**Evan** raised it). No JIT and the `clock` gate are this round's measures. They make timing attacks harder; they do not make them impossible, and the doc says so: nothing in-process is a real boundary against a speculative read of the same address space. A strong boundary for computer use is a separate process per task, which `sandbox` makes possible later.

### Content

12. **The content tree** (**Evan**, shape mine). `content.Node` carries `Kind` (`Page`, `Image`, `Animation`, `Video`, `Text`, `Unknown`), `MIME`, `Size` (−1 unknown), `URL`, `Poster`, `Width`, `Height`, and `Children`. A page's DOM stays the DOM; a `Page` node refers to it and has a child for each media element a surface may present separately. Direct navigation to an `.mp4` is a `Video` node with no parent.
13. **Classification uses headers, before the body.** A new netpolicy `Fetcher.Open` returns status, final URL and headers with the body unread, under every existing check; `Do` is built on it. `content.Classify(mime, url)` decides:
    - `text/html`, `application/xhtml+xml` → `Page`
    - `image/gif`, `image/webp` → `Animation` (a still image once decoded if single-frame)
    - other decodable images → `Image`
    - `video/*` → `Video`
    - other `text/*`, JSON → `Text`
    - anything else, or an unreadable type → `Unknown`
    
    The server's type is believed; a missing type or `application/octet-stream` goes by the URL's extension for media only, never to `Page`.
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

20. **Only a reader action starts video** (**Evan**: opt-in, click-to-play). Activating a link that resolves to a video, or a focused `<video>` element, starts it. Script cannot: there is no `play()` binding, and `autoplay` is ignored. A `<video>` with a `src` or a `<source>` child becomes focusable only when `media=video`; the first `<source>` whose `type` classifies as `Video` wins. With video off, activating one shows the status line `video is off in this profile`.
21. **The player** is an `ntvideo.Model` filling the viewer's area, with `WithScrubber(true)`, in the app. Keys: Space pause, ←/→ seek 5 s, `m` mute, Esc back, Ctrl+C quit. All messages are forwarded to it while open. A page keeps running beneath it, and frames are not shown. Without libmpv, ntvideo's own message shows and Esc still returns.
22. **Download first, then play.** The file comes through netpolicy, the page's own origin rules and the `navigate` gate, into a private directory (`os.MkdirTemp`, mode 0700, random file name, no extension from the URL), and ntvideo gets that local path. mpv never sees a URL, so every network request stays under netpolicy. The status line shows progress. The file is deleted when the player closes and the directory at exit; a crash leaves it in the OS temp area (accepted).
23. **ntvideo is compiled in by default and removed with `-tags nomedia`,** as CJK is with `nocjk`. Its only import is in a build-tagged file in `app`. With `nomedia`, `media=video` fails validation, and `-show-profile` reports `compiled-out`. libmpv is loaded only when a video first plays, never at startup, and never when `native-code=off`.

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
func (p Profile) Validate() error
func (p Profile) Table(s Strengths) string

// netpolicy
func (f *Fetcher) Open(ctx context.Context, r Request) (*OpenResponse, error) // headers checked, body unread
// KindNavigate admits any content type; the caller classifies.

// content: no system imports, builds for wasm.
func Classify(mime, rawURL string) Kind
type Node struct { /* decision 12 */ }

// sandbox: per platform, imported only by cmd/ntbrowser.
func Apply(p profile.Profile, start string) (Strengths, error)
```

`app.New` and `loop.Config` take the `Profile` in place of the flags they take today; the subresource and fetch policies are derived from it.

## 7. Threat model additions

- **A hostile page or agent inside the sandbox:** it has only the JS surface; the gates are not reachable from it, and OS enforcement backs the file and process gates.
- **Hostile media:** images and animations go through pure-Go decoders with limits. Video reaches C decoders only after a reader action in a profile that opened `native-code`, and the OS sandbox still applies to that process. This is the main risk the round adds, and it is why video is in no built-in.
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
- Video: no reader action, no player; script cannot start it; `autoplay` ignored; `nomedia` build fails validation; temp file gone after Esc; libmpv not loaded when `native-code=off`.
- A fake `Surface` proves a presenter can choose a lesser presentation, and that a refused node never reaches it.
- Live: nimbleterminal.com, activating a recording, plays under `-media video`, and shows the refusal line under `browse`.
- `GOOS=js GOARCH=wasm` builds `profile` and `content` along with the existing list.

## 9. Left to the builder

The exact seccomp list, the generated macOS policy text, Go type names for gate values, wording of status lines, whether `Text` wraps at the terminal width, and how the progress line looks.
