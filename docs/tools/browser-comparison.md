# Comparing Chrome and glynx

`glynx-compare` captures a public HTML URL in a fresh headless Chrome and the
same browser loop used by glynx. It writes an HTML report with side-by-side
viewport screenshots, an exact pixel-difference image, text, geometry and
network/script diagnostics. Nothing is published.

```sh
GOWORK=off CGO_ENABLED=0 go run ./cmd/glynx-compare \
  --out /tmp/rust-comparison \
  --width 1200 --height 900 --scheme light \
  https://blog.rust-lang.org/2026/11/12/Rust-1.100.0/

# Equivalent task wrapper; flags must precede the URL.
task compare -- --out /tmp/rust-comparison-2 --scripts=false \
  https://blog.rust-lang.org/2026/11/12/Rust-1.100.0/
```

Open the resulting `index.html`. The output directory must not already exist;
its parent must exist. Without `--out`, the command chooses a timestamped
folder in the system temporary directory. Directories are owner-only and
artifacts use mode 0600. Chrome is auto-detected in standard macOS/Linux
locations; override with `--chrome /path/to/chrome` or `CHROME_BIN`.

Options include `--width` (320–2048), `--height` (200–2048), `--scheme`
(light/dark), `--scripts=false`, `--settle` (default 2s) and `--timeout`
(default 60s, maximum 5m). Device scale is explicitly 1. Matching a terminal
session requires its **CSS layout width**, not its terminal column count or
physical screenshot width; glynx's `--scale` affects that conversion.

## What is compared

- Initial HTML is fetched once through `netpolicy`, saved as `source.html`,
  hashed, and supplied to both engines at the final response URL. HTTP status,
  content type, CSP and X-Frame-Options are retained for Chrome. Other response
  headers, including initial Set-Cookie, are not replayed.
- Styles, images, fonts and scripts load live and independently. This is not
  an archived/offline replay; content negotiation, changing resources and
  cookie-dependent responses can differ. Assets requested and failures appear
  in `report.json`; it does not claim identical asset bytes.
- The glynx side uses `browsercmd.Glynx` limits, the glynx profile with disk
  persistence off, `app.SubresourceTransport`, the normal loop, and its serial
  painter. It uses the **published nthtml version in go.mod**. No sibling
  replacements, source rewriting or compatibility polyfills are applied.
- Chrome uses a new temporary profile and pipe-based CDP, adapted from
  nthtml's fixed-fixture differential harness. It does not attach to the
  user's browser or expose a debugging TCP port. Its normal sandbox remains
  enabled. Measurement runs in an isolated world to avoid page replacements
  of DOM methods.
- The glynx snapshot is the latest frame two seconds after its first frame;
  Chrome waits two seconds after load, then up to two seconds for fonts.
  These are bounded settling heuristics, not proof that all asynchronous work
  is complete. Increase `--settle` when investigating delayed content.
- The **Image findings** section matches up to 128 `<img>` elements per engine
  by absolute resource URL, including images without IDs. Glynx observations
  come from the serializer snapshot for the captured layout generation, with
  child realms excluded. Source anchors associate images with painted boxes;
  multiple images sharing an anchor remain unavailable instead of guessing
  box order. Repeated URLs, missing/undecoded images, clipped images, and
  images outside the screenshot are explicitly marked, not counted as passes.
  Visible crops are aligned locally, preserving their original sizes. Different
  sizes are labelled `SIZE_DIFFERS`; equal-sized crops get a pixel difference
  and `PIXELS_MATCH` / `PIXELS_DIFFER`. Crop allocation is capped at eight
  million pixels. SVG backgrounds and inline SVG are not `<img>` resources.
  Differences can still include CSS, background, occlusion, antialiasing and
  changing remote bytes; use the crops to select an isolated regression.
- Geometry is matched only by IDs among the first 512 selected Chrome
  elements. Duplicate IDs within that sample and absent painted boxes are
  marked unavailable. Glynx's ID-associated painted coverage is not always a
  CSS border box, so a coverage difference is not automatically a layout
  defect. Raw boxes and Chrome font properties are also retained.
- `glynx.txt` is semantic reading output; `chrome.txt` is `innerText`. They are
  useful for finding lost content, but are not equivalent text serializers.

This is a developer host, not the confined interactive executable: the glynx
side retains VM, network and resource gates but does not install OS process
confinement. It does not compare terminal/Kitty transport, scroll interaction,
keyboard tasks, persisted logins, or media playback. Script navigation requests
from glynx are reported rather than followed; check Chrome's captured URL for
navigation before treating screenshots as comparable. A complete capture exits
successfully even when rendering differs; capture failures return nonzero and
preserve a partial report when possible.

No global pixel threshold establishes usability: font substitution,
antialiasing and emoji can produce a large difference despite readable content.
Review the screenshots and resource diagnostics first, then reduce individual
layout defects to focused fixtures.

## Tests

```sh
GOWORK=off CGO_ENABLED=0 go test ./internal/compare ./cmd/glynx-compare
# Optional integration test needs a locally installed Chrome.
GOWORK=off CGO_ENABLED=0 GLYNX_COMPARE_CHROME_TEST=1 go test ./internal/compare
```

The ordinary tests exercise the real loop with script-on/off documents, exact
1200×900 viewport painting (past the terminal's column ceiling), geometry
ambiguity, report escaping, and image-difference accounting. They require no
live website or Chrome installation.

## First case: Rust 1.100.0 announcement

A 1200×900, light, scripts-on capture on 2026-11-13 reproduced the reported
mismatch with published nthtml v0.6.0:

- Chrome's navigation occupies one 80px-high row. Glynx places links below the
  brand; the article section starts at y=132 instead of Chrome's y=80.
  The site uses a wrapping flex container with a `flex-auto` brand and
  `flex-none` navigation. Reduce that interaction before attributing the
  precise defect to a particular flex rule.
- Chrome fetched Alfa Slab One and Fira Sans WOFF2 files. Glynx made no font
  requests and displayed fallback typography; the heading widths and line
  breaks differ accordingly.
- Glynx paints the Rust logo as a black gear silhouette and omits the paintbrush
  glyph shown by Chrome. SVG rendering and glyph fallback need separate
  investigation; neither was diagnosed solely from pixel-difference counts.
- Both captures completed without reported script errors. Total document
  heights were 10,717px and 10,692px respectively; the similar total heights
  conceal substantial local layout differences.

The observed pixel difference was about 20.9%; it is evidence for this capture,
not a passing/failing baseline. Live page assets can change. Screenshots and
full page contents are generated locally rather than committed into the repo.

## Rust logo diagnosis and local fix

The image-level report now identifies `images/rust-logo-blk.svg` as
`PIXELS_DIFFER` (about 18.3% of its 80×80 crop in the measured capture), with
side-by-side logo crops. The entire-page percentage no longer has to stand
in for a diagnosis of this small asset.

The SVG's outer gear uses `fill-rule="evenodd"`. The published parser ignores
that property, and the published rasterizer's `SetWinding(false)` is a no-op.
The gear's filled interior therefore paints over the R. Local fixes in the
sibling `oksvg` and `rasterx` repos add inherited/cascaded fill-rule parsing
and an antialiased parity scan converter. Nonzero fills retain their existing
rendering path. Tests cover nested holes, overlapping and self-intersecting
contours, implicit closure, clipping, gradients, fractional edges and rule reset.

The actual Rust SVG was parsed with the local fixed parser; its flattened draw
commands and winding flags were replayed through the local fixed rasterizer.
The resulting logo restores the R and interior details. This checks the two
local fixes together without replacing any dependency in ntbrowser.

**Adoption is still pending published dependency versions.** The repository
continues to use published oksvg v0.1.6 and rasterx v0.1.0, so a normal glynx run
still reproduces the defect. No module-cache files, module pins, sibling
replacements, releases or pushes were used to conceal that distinction. Once
releases are authorized, publish the renderer fixes and update the dependency
pins, notices and regression baselines before calling the interactive browser
fixed. The comparison/report improvements work immediately.
