# CSS-preserving viewport rendering

Status: upstream implementation committed on nthtml branch
`feat/host-painted-viewport` at `9bb6fd6` (initial implementation `24f51fa`); not yet published or adopted by
ntbrowser. Semantic `-chunked` mode ships independently. Date: 2026-11-05.

## Implementation checkpoint

The upstream branch adds `ModelOptions.HostPainted`, `PlanPaint`, atomic
`SetPaintedResult`, cancellable `PaintRequest`/`PaintReady` messages, band-local
search/selection/focus overlays, and global viewport/hit-test mapping. A reusable
`document.Painter` provides bounded font caches and a y-bucket operation index.
The integration contract and compiled example live in nthtml's
`docs/host-painted.md` and `model_paint_example_test.go`.

Native tests, vet, builds, and the WASM compile check passed. Regression coverage
includes stale completions, error recovery, mode changes, source focus, fractional
scales, pixel budgets, 64x zoom, Kitty zlib/StableID behavior, and equivalence to
`Result.Paint` for styled content. On one Apple M5 Max sample, a warm 400×200 band
on a 1,100-paragraph layout took 0.491 ms versus 0.609 ms uncached, with 174 versus
248 allocations. Cold indexing took 0.683 ms. Production CJK font throughput
remains to be measured; synthetic host-fallback correctness is covered.

Host-painted mode supports width fit. Paint scale is clamped while logical zoom
is retained; the transport fills the planned grid to absorb raster rounding
without scale-dependent letterboxes. Existing viewer callers retain their API
and behavior. These choices are documented in the upstream integration contract.

Next: publish the upstream API, then implement the ntbrowser adoption checklist
below using that published dependency. The default CSS renderer still uses
`NoImage: false`; this checkpoint does not supersede that current requirement.

Review fixes in `9bb6fd6` track the submitted picture plan/revision separately,
force upload after a TTY-mode replacement, reconcile stale install geometry, and
preserve sub-row pan input. Column changes resize the retained band immediately
and keep hit mapping aligned if replacement painting fails. Regression tests
cover these cases, including Kitty image-payload comparison. Initial paint plans
use automatic scale (`Scale: 0`), including glyph transport.

## Problem

nthtml v0.3.0 adds `typeset.BuildChunked`, backed by nttypeset v0.2.0's
`Engine.NewChunked`. This is an opt-in semantic HTML adapter, not a chunked
implementation of nthtml's CSS renderer. Switching ntbrowser to it changes CSS
behavior, source geometry, and selection semantics. In particular, the adapter
ignores CSS background images and repeats ancestor decoration across chunks.

ntbrowser's existing CSS renderer builds a complete `document.Result`, including
a whole-page raster, on its render worker. nthtml's viewer can transmit bands of
that raster to Kitty, but the whole raster remains resident. Terminal transport
banding does not bound the source raster allocation.

## Existing upstream primitives

- `Document.Render` with `Options.NoImage` retains CSS layout and display lists
  without allocating the complete page raster. `Limits.LayoutHeight` bounds it.
- `Result.Paint(ctx, area, scale)` rasterizes a bounded region in document
  coordinates. Its pixel budget applies to each paint call.
- `Result` retains global links, boxes, source anchors, and text indexes.
- `Model.CellToDocument` and `Model.DocumentViewport` already account for Kitty
  bands, viewport offsets, zoom, scaling, and letterboxing.

The gap is the viewer's source contract: `NewWithOptions` and `SetResult` try to
paint the entire result synchronously when `Result.Image` is absent. Its band
logic slices a complete source image rather than requesting regional painting.

## Proposed nthtml viewer contract

Add an explicit host-painted mode; existing callers retain their behavior. Keep
document extent separate from the bounds of the resident raster. This requires
an nthtml release before ntbrowser can enable the CSS path.

### Atomic layout and initial band

Install a layout **with its initial painted band**, never a layout followed by a
separate first-paint round trip. The loop already knows the initial viewport's
width and top-of-page origin; it must also receive the viewport height/terminal
geometry needed to size the initial band. In one worker pass, build the layout
with `NoImage`, plan its initial band with automatic scale (`Scale: 0`), paint
at the returned scale, and send the result, image,
CSS band rectangle, scale, and revision together. Reflow uses the latest requested
viewport, clamped to the new extent. Installing this pair must never invoke
`Paint` or allocate a whole-page image on the UI goroutine.

While a replacement layout is being prepared, retain the previous layout/raster
pair. Do not dispatch old-page actions into a newer DOM: ntbrowser must gate
input by the presented frame revision. A first-load failure displays an error;
a replacement failure retains the old pair and reports the error.

### Later paint requests and pending presentation

The viewer emits immutable requests containing owner, layout revision, request
sequence, CSS rectangle, and raster scale. The host paints with a deadline on a
worker and returns the request identity plus image or error. Accept completions
only for the installed revision and latest request sequence. Superseding
navigation/reflow invalidates the revision; another scroll/zoom invalidates the
request sequence. Cancel superseded work. Closing invalidates all work.

For a scroll beyond the resident band, keep the old band **and its displayed
viewport** until the requested band is ready. Maintain the desired viewport
separately, coalescing further scroll input. Atomically replace the band and
presented viewport on completion; hit-testing uses presented geometry throughout.
On paint failure keep the old presentation and report the error, allowing retry.

### Band sizing and scale

Start with the viewer's existing `maxKittyBandRows` rule and look-ahead/behind
placement. Cap band rows by both `ntpicture.MaxImagePixels` and the picture's
`MaxRasterPixels`, divided by raster pixels per terminal row. Also cap the
source band's scaled allocation by `Result.Paint`'s document pixel limit. Apply
these caps before allocation, including rounding at the rectangle edges. Glyph
transport uses the same bounded-source contract with its own raster geometry.

Preserve the viewer's current zoom range. Clamp **paint scale**, not logical
zoom, to `[1/16, 16]`; the transport performs the remaining resampling. At 64x
zoom paint at 16x and upscale by four. For a 4096 px page fitted into 80 glyph
columns, paint at 1/16 and downsample to the glyph raster. Keep logical CSS
coordinates and actual raster dimensions explicit so hit-tests, overlays and
rounding use the same transform. Pixel caps may reduce band height; they must
not silently change logical zoom. Retain StableID and zlib transport behavior.

### Overlay rewrite

Replace the current whole-source-copy path (`paintSelection` in
`model_links.go`) with **band-local compositing**. Search matches, selection,
source focus and link overlays project their global geometry into the resident
band. Copy only that bounded band when an overlay needs a writable raster, and
reapply overlays after a new band arrives. No overlay may paint or copy the
whole document. This is explicit upstream implementation work, not merely a
caller constraint. Include both Kitty focus patches and glyph fallback.

### Paint performance

Today each `Result.Paint` rebuilds face/font maps, reparses OpenType fonts, and
walks the entire display list. Bounded raster allocation alone does not bound
band-crossing latency with respect to page length.

Add a painter tied to an immutable layout revision, with bounded font/face
caches surviving individual paint calls and a vertical bucket index over
operations. Preserve paint order when gathering intersecting operations;
account for ink overflow and clipping. Very tall operations need a bounded
shared list rather than duplication into every bucket. The painter is owned by
one serial worker; cancellation and cache ceilings remain explicit. Benchmark
the existing painter first, then verify cold and warm band crossings on short
and near-limit layouts, including font-heavy/CJK pages. Report latency and
allocations, not just visual correctness; the index must eliminate repeated
full-op scans for ordinary narrow bands.

### ntbrowser adoption and superseded rules

Keep layout and paint off the UI and VM goroutines. Retain DOM snapshots,
security gates, subresource allowlists and global source identities. When the
upstream release is integrated, update these exact contracts together:

- `AGENTS.md`: the `document.Options.NoImage stays false` constraint and the
  viewer/Kitty transport requirement.
- `docs/design/2026-10-25-ntbrowser-architecture-design.md`: §3's whole-image
  worker rule and decision 19; describe atomic layout-plus-band delivery.
- `app/app.go`: `viewerOptions` commentary and frame installation/scroll restore.
- `loop/loop.go`: render worker options and the atomic frame payload.
- `app.TestViewerKeepsKittyIDAndSendsZlib` in `app/viewer_test.go`: adapt its setup to
  the host-painted API, retaining its StableID/zlib assertions. Add asynchronous
  band and stale-completion tests rather than weakening this transport gate.

The current semantic mode does not change the default CSS path's `NoImage`
rule and does not need this future upstream interface.

## Limits and validation

This removes the whole-page **raster** limit; it does not make parsing or CSS
layout lazy. The full DOM and display list remain bounded. A document exceeding
those limits must still fail. Truly lazy CSS layout needs a separate design for
cross-boundary dependencies such as floats, tables, and positioned content.

Validate long styled pages with CSS backgrounds and positioned content; bounded
paint allocations; scroll/band crossings; source focus, fragments, selection,
and hit-testing at nonzero offsets and zoom; resize and DOM mutation; late paint
completion after navigation; cancellation and budget errors; Kitty ID reuse and
zlib frames; glyph fallback below 1/16 scale and zoom through 64x; cold/warm
paint latency and allocations near the layout limit; and the WASM compile gate.

Ship the semantic chunk adapter separately as opt-in `-chunked` reading mode,
with reduced CSS support explicit in its help and documentation. It is not
transparent CSS-preserving adoption of the new dependencies.
