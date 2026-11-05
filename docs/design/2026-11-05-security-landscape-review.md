# Security landscape boundary review

Review date: 2026-11-05. Companion to the
[website draft](2026-11-05-nimbleterminal-security-landscape.md).

## Purpose and scope

The landscape should explain the existing library boundaries and their
composition. It should not present application integration gaps as missing
library functionality, or imply that all Nimble Terminal packages use workers
by default. This review covers the document/media stack and the two applications
under discussion. It is not an audit of every Nimble Terminal repository.

Sources are local working trees. nthtml and nttypeset are being exercised before
release. Gloss has existing uncommitted work; its observations describe the
inspected paths, not a verified release. No library code or website code was
changed for this review, and no new security test campaign was run.

## Source map

Paths below are relative to the shared projects directory.

| Repository HEAD | Evidence inspected | Claim supported |
| --- | --- | --- |
| ntconfine `124b707` | `SECURITY.md`, `confine.go`, `helper_unix.go` | OS policy, trusted executable model, support reporting, deadlines and helper lifecycle |
| ntmediabox `5b5179b` | `SECURITY.md`, `mediaproc.go`, `render.go`, `pkg/mediabox/` | Two protocols; wire checks; kill-hook ownership; raw metadata remains untrusted |
| ntpicture `3da99c6` | `README.md`, `decoders/worker/worker.go`, `animation/` | Still-image worker is explicit; animation decoding is a different path |
| ntsvg `3e38282` | `README.md`, `worker.go`, worker regression tests | Persistent SVG worker, metadata checks, coordinated limits and no host metadata parsing fallback |
| ntpdf `64861ed` | `README.md`, `SECURITY.md`, worker metadata and security tests | Worker covers PDF parsing and metadata as well as rasterization; default wazero path differs |
| nttypeset `fbc6ecd` | `SECURITY.md`, `worker.go` | Builder location matters; preconnected client transfers lifecycle ownership; geometry/link validation |
| ntvideo `a030ec3` | `SECURITY.md`, `worker.go`, `headless.go` | Explicit streaming/headless modes, staging, narrowed request limits, no universal streaming deadline |
| nthtml `ba1e3de` | `README.md`, `document/diagram.go`, `image.go`, `background.go`, `document.go` | Parsing/layout limits and local diagram/data-URL decode entry points |
| ntbrowser `553e52a` | `app/content.go`, `app/player.go`, `subres/decode.go`, `loop/loop.go`, `cmd/ntbrowser/main.go`, profiles design | Native worker, startup-only launch, host admission, remaining in-process rendering |
| gloss `9e80fb5` plus working changes | `internal/document/parser_worker*.go`, `loader.go`, `gif.go`, `internal/app/video*.go` | Parser subprocesses, unconfined fallback, normal raster/animation paths, opt-in video worker |

## Integration decisions to make together

1. **Reuse library worker paths.** Prefer format-specific APIs and their metadata
   validators over a shared application protocol that duplicates them. Where an
   adapter is needed, it should preserve each library's limits and failure rules.
2. **Separate availability from enforcement.** A bounded in-process decoder, an
   unconfined child, and an OS-confined worker are distinct modes. Applications
   need an explicit requirement and a truthful report of the selected mode.
3. **Choose worker lifetime in the application.** Gloss can create replacements
   on demand. ntbrowser must start workers before lockdown; a finite pool with
   degradation or a separately authorized supervisor are different designs.
   Do not silently relax `process=off` to reuse a convenience launcher.
4. **Cover the complete input path.** Include SVG/Mermaid conversion, embedded
   raster images, CSS data URLs, captions, metadata-only views, export, and
   animation. A worker on the final raster call leaves earlier parsing local.
5. **Keep formats and transport separate.** Raw RGBA responses remove the need
   to decode worker-generated PNG in the host. Structured metadata still needs
   its library's semantic checks, and URLs still need host admission.
6. **Preserve least privilege.** A pure-Go render worker should not acquire
   native decoder permissions or broad source-directory access merely because
   video uses a worker too. Embedded trusted fonts need no new permission;
   host-provided font bytes or callbacks require their own boundary analysis.

Animation worker coverage and complete nthtml document transport need further
design. The latter must carry the boxes, anchors, links, and other metadata
ntbrowser uses; substituting nttypeset's raster result would lose that contract.
Office/archive/mesh processing, browser bridges, and terminal parsers merit
separate detailed reviews before claiming ecosystem-wide coverage.

## Website integration proposal

The local `NimbleTerminal-website` repository already serves contribution content
from one source as HTML, Markdown, and part of `llms.txt`. Use the same approach
for `/security` and `/security.md`, with a short linked entry in `llms.txt`.
The existing dependency explorer is a useful cross-link, but a dependency edge
must not be presented as proof that the dependent application enables a worker.

Before publication, align the matrix with released versions, replace moving
source links with the intended release policies, and confirm the shared private
reporting contact. Keep the detailed syscall rules and exact budget defaults in
project policies. The website should explain the boundaries and link to those
contracts, so it does not become a second, stale specification.

The draft includes Mermaid source for the boundary diagram. The website will
need to render or translate it into its own component and verify the table and
diagram on narrow screens. Website implementation and deployment are not part
of this documentation change.
