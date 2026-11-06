// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"fmt"
	"html"
	"image"
	"strings"
	"time"

	"nimbleterminal.dev/ntfonts"
	"nimbleterminal.dev/nthtml/document"
	htmltypeset "nimbleterminal.dev/nthtml/typeset"
	"nimbleterminal.dev/nttypeset"
)

// ChunkInfo describes the chunk the frame belongs to. All result coordinates,
// search matches and selections are local to Index. Count == 0 is the CSS path.
type ChunkInfo struct {
	Index, Count int
	Anchor       string // requested fragment or focus to reveal after installing the frame
	Bottom       bool   // entering the previous chunk at its end
}

type chunkRequest struct {
	index  int
	anchor string
	bottom bool
	seq    uint64
}

// chunkRender is exclusively owned by the serial render worker. The VM only
// transfers its pointer between workers; it never reads the engine or layouts.
type chunkRender struct {
	source *mappedChunks
	cache  *nttypeset.Chunked
	width  int
	doc    *document.Document
	index  int
	place  string
	recent []int
}

type mappedChunks struct {
	*htmltypeset.ChunkSource
	maps map[int]*htmltypeset.SourceMap
}

func (s *mappedChunks) Chunk(ctx context.Context, i int) (*nttypeset.Document, error) {
	d, sm, err := s.ChunkWithSources(ctx, i)
	if err == nil {
		s.maps[i] = sm
	}
	return d, err
}

type chunkFonts struct{ base document.FaceSource }

func (f chunkFonts) Face(family, variant string) ([]byte, error) {
	for _, fallback := range fallbackFaces {
		if fallback.Family == family {
			return fallback.Source.Face(family, variant)
		}
	}
	if f.base != nil {
		return f.base.Face(family, variant)
	}
	return ntfonts.Embedded.Face(family, variant)
}
func (f chunkFonts) Fallbacks() []string {
	var names []string
	for _, fallback := range fallbackFaces {
		names = append(names, fallback.Family)
	}
	return names
}

func newChunkRender(ctx context.Context, doc *document.Document, opts document.Options) (*chunkRender, error) {
	// Small semantic units keep each displayed raster bounded. Atomic blocks
	// that cannot fit are errors, never truncated or split mid-element.
	src, err := htmltypeset.BuildChunked(ctx, doc, htmltypeset.ChunkOptions{
		Options: htmltypeset.Options{Document: opts},
		Limits:  document.TypesetChunkLimits{Nodes: 256, TextBytes: 16384},
	})
	if err != nil {
		return nil, err
	}
	pixels, height := chunkRasterLimits(opts.Limits)
	eng, err := nttypeset.NewEngine(nttypeset.WithFonts(chunkFonts{opts.Fonts}), nttypeset.WithLimits(nttypeset.Limits{
		Pixels: pixels, Height: height, Deadline: 2 * time.Second,
		ShapeCache: 8 << 20, GlyphCache: 8 << 20,
	}))
	if err != nil {
		return nil, err
	}
	source := &mappedChunks{ChunkSource: src, maps: make(map[int]*htmltypeset.SourceMap)}
	cache, err := eng.NewChunked(source)
	if err != nil {
		return nil, err
	}
	return &chunkRender{source: source, cache: cache, width: opts.Width, doc: doc}, nil
}

func chunkRasterLimits(lim document.Limits) (pixels, height int) {
	pixels, height = document.DefaultLimits.Pixels, document.DefaultLimits.Height
	if lim.Pixels > 0 {
		pixels = min(pixels, lim.Pixels)
	}
	if lim.Height > 0 {
		height = min(height, lim.Height)
	}
	return
}

func (r *chunkRender) render(ctx context.Context, req chunkRequest, opts document.Options) (*document.Result, ChunkInfo, error) {
	info := ChunkInfo{Index: max(0, min(r.cache.Chunks()-1, req.index)), Count: r.cache.Chunks(), Anchor: req.anchor, Bottom: req.bottom}
	if req.anchor != "" {
		start, end, ok := r.source.ChunkRange(req.anchor)
		if !ok {
			info.Anchor = ""
		}
		if ok && (info.Index < start || info.Index >= end) {
			info.Index = start
		}
	}
	previousMap, hadMap := r.source.maps[info.Index]
	chunk, err := r.cache.At(ctx, info.Index, opts.Width, nttypeset.LayoutOptions{})
	// Source maps follow the same two-entry LRU as the layout cache.
	if err != nil {
		if hadMap {
			r.source.maps[info.Index] = previousMap
		} else {
			delete(r.source.maps, info.Index)
		}
		return nil, info, err
	}
	recent := []int{info.Index}
	for _, i := range r.recent {
		if i != info.Index && len(recent) < 2 {
			recent = append(recent, i)
		}
	}
	for i := range r.source.maps {
		keep := false
		for _, j := range recent {
			keep = keep || i == j
		}
		if !keep {
			delete(r.source.maps, i)
		}
	}
	r.recent = recent
	sm := r.source.maps[info.Index]
	if err := chunk.Layout.Complete(ctx); err != nil {
		return nil, info, err
	}
	pixels, maxHeight := chunkRasterLimits(opts.Limits)
	height := max(1, chunk.Layout.Extent())
	if height > maxHeight || opts.Width > pixels/height {
		return nil, info, fmt.Errorf("%w: chunk raster", document.ErrLimit)
	}
	view := nttypeset.View{Width: opts.Width, Height: height, Scale: 1}
	img, err := chunk.Layout.Raster(ctx, view, nttypeset.RasterOptions{Palette: sm.Palette()})
	if err != nil {
		return nil, info, err
	}
	res := &document.Result{Image: img, Anchors: make(map[string]image.Point)}
	r.place = ""
	for _, id := range sm.IDs() {
		a, ok, err := chunk.Layout.AnchorBounds(ctx, id, false)
		if err != nil {
			return nil, info, err
		}
		if ok && !a.Rect.Empty() {
			res.Anchors[id] = a.Rect.Min
			res.Boxes = append(res.Boxes, document.Box{Bounds: a.Rect, Tag: "section", Anchor: id, SourceID: id})
			// Prefer a block ID to a container that spans several chunks.
			start, end, _ := r.source.ChunkRange(id)
			if r.place == "" && end-start == 1 {
				r.place = id
			}
		}
	}
	bounds := chunk.Layout.Bounds()
	for i, b := range chunk.Document.Blocks {
		if i >= len(bounds) || bounds[i].Empty() {
			continue
		}
		id, _, err := chunk.Layout.SourceAt(ctx, nttypeset.Place{Block: i, Placed: true})
		if err != nil {
			return nil, info, err
		}
		res.Boxes = append(res.Boxes, document.Box{Bounds: bounds[i], Tag: "text", Text: chunkText(b), TextPrefix: "\n", SourceID: id, FontSize: max(1, b.Size)})
	}
	// Keep separate hit runs instead of merging all occurrences of one URL.
	for _, link := range chunk.Layout.Links(view) {
		label := link.URL
		selection, err := chunk.Layout.Select(ctx, nttypeset.Selection{
			Anchor: nttypeset.Place{Block: link.Block, Para: link.Para, Rune: link.Runes[0], Placed: true},
			Focus:  nttypeset.Place{Block: link.Block, Para: link.Para, Rune: link.Runes[1], Placed: true},
		})
		if err != nil {
			return nil, info, err
		}
		if selection != nil && selection.Text() != "" {
			label = selection.Text()
		}
		res.Links = append(res.Links, document.Link{ID: len(res.Links) + 1, Href: link.URL, Text: label, Regions: []image.Rectangle{link.Rect}})
	}
	// The viewer's TTY mode must only see this chunk, not reflow the original
	// whole document. Escaped semantic markup preserves text/links/source IDs.
	ttyDoc, err := document.Parse(ctx, strings.NewReader(chunkHTML(chunk.Document)), opts.Limits)
	if err != nil {
		return nil, info, err
	}
	r.index = info.Index
	return res.WithDocument(ttyDoc, opts), info, nil
}

func chunkText(b nttypeset.Block) string {
	var s strings.Builder
	for _, span := range b.Spans {
		s.WriteString(span.Text)
	}
	for _, row := range b.Rows {
		for i, cell := range row {
			if i > 0 {
				s.WriteByte('\t')
			}
			for _, span := range cell {
				s.WriteString(span.Text)
			}
		}
		s.WriteByte('\n')
	}
	return s.String()
}

func chunkHTML(doc *nttypeset.Document) string {
	var s strings.Builder
	spans := func(spans []nttypeset.Span) {
		for _, span := range spans {
			href := document.SafeURL(span.Href)
			if href != "" {
				fmt.Fprintf(&s, `<a href="%s">`, html.EscapeString(href))
			}
			s.WriteString(html.EscapeString(span.Text))
			if href != "" {
				s.WriteString("</a>")
			}
		}
	}
	s.WriteString("<body>")
	for i, b := range doc.Blocks {
		for _, section := range doc.Sections {
			if section.Start == i {
				fmt.Fprintf(&s, `<section id="%s">`, html.EscapeString(section.SourceID))
			}
		}
		tag := "p"
		if b.Kind == nttypeset.KindCode {
			tag = "pre"
		}
		fmt.Fprintf(&s, `<%s id="%s">`, tag, html.EscapeString(b.SourceID))
		spans(b.Spans)
		for _, row := range b.Rows {
			for j, cell := range row {
				if j > 0 {
					s.WriteByte(' ')
				}
				spans(cell)
			}
			s.WriteString("<br>")
		}
		fmt.Fprintf(&s, "</%s>", tag)
		for j := len(doc.Sections) - 1; j >= 0; j-- {
			if doc.Sections[j].End == i+1 {
				s.WriteString("</section>")
			}
		}
	}
	s.WriteString("</body>")
	return s.String()
}

// MoveChunk requests a bounded, lazy jump. end is -1 for the first chunk,
// +1 for the last, or zero for a relative delta. gen rejects stale UI input.
func (l *Loop) MoveChunk(gen uint64, delta, end int) {
	l.tryPost(func() {
		p := l.page
		if p == nil || p.frameGen != gen || p.chunkInfo.Count == 0 {
			return
		}
		i := p.chunkReq.index + delta
		if end < 0 {
			i = 0
		}
		if end > 0 {
			i = p.chunkInfo.Count - 1
		}
		i = max(0, min(p.chunkInfo.Count-1, i))
		if i == p.chunkReq.index {
			return
		}
		p.chunkReq = chunkRequest{index: i, bottom: delta < 0, seq: p.chunkReq.seq + 1}
		l.pendingFrame = true
	})
}

// RevealChunk routes a fragment or focus through the index without laying out
// intervening chunks. The worker resolves it against the latest DOM snapshot.
func (l *Loop) RevealChunk(gen uint64, anchor string) {
	l.tryPost(func() {
		p := l.page
		if p == nil || p.frameGen != gen || p.chunkInfo.Count == 0 || anchor == "" {
			return
		}
		p.chunkReq.anchor, p.chunkReq.bottom = anchor, false
		p.chunkReq.seq++
		l.pendingFrame = true
	})
}

// ChunkRange only reads the immutable index, so the VM may use this narrow
// interface while the worker lays out another chunk. It never shares an engine
// or a mutable source-map cache. Inline IDs can have membership without geometry.
type chunkLookup interface{ ChunkRange(string) (int, int, bool) }

func (p *Page) chunkContains(id string) bool {
	if p.chunkIndex == nil {
		return false
	}
	start, end, ok := p.chunkIndex.ChunkRange(id)
	return ok && p.chunkInfo.Index >= start && p.chunkInfo.Index < end
}
