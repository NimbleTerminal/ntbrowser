// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package subres

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"image"
	"mime"
	"net/url"
	"strconv"
	"strings"

	"nimbleterminal.dev/nthtml/document"

	_ "golang.org/x/image/webp" // registers the webp decoder with image
)

const (
	svgDefaultSide = 64
	svgMaxSide     = 1024
)

// svgSize reads the root <svg> width/height (px or unitless), else the
// viewBox, defaulting to 64×64 and clamping each side to [1, 1024].
func svgSize(src string) (w, h int) {
	w, h = svgDefaultSide, svgDefaultSide
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "svg" {
			break
		}
		var aw, ah, vw, vh float64
		for _, a := range start.Attr {
			switch a.Name.Local {
			case "width":
				aw = parseSVGLength(a.Value)
			case "height":
				ah = parseSVGLength(a.Value)
			case "viewBox":
				if f := strings.Fields(strings.ReplaceAll(a.Value, ",", " ")); len(f) == 4 {
					vw, _ = strconv.ParseFloat(f[2], 64)
					vh, _ = strconv.ParseFloat(f[3], 64)
				}
			}
		}
		switch {
		case aw > 0 && ah > 0:
			w, h = int(aw+0.5), int(ah+0.5)
		case vw > 0 && vh > 0:
			w, h = int(vw+0.5), int(vh+0.5)
		case aw > 0:
			w, h = int(aw+0.5), int(aw+0.5)
		case ah > 0:
			w, h = int(ah+0.5), int(ah+0.5)
		}
		break
	}
	return clampSide(w), clampSide(h)
}

func parseSVGLength(v string) float64 {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	if strings.HasSuffix(v, "%") {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return f
}

func clampSide(n int) int { return max(1, min(svgMaxSide, n)) }

// decodeFetched turns a fetched image body into an image: SVG through
// nthtml's bounded diagram renderer at the SVG's own size, everything else
// through document.DecodeImage (GIF/JPEG/PNG, plus webp registered above).
func decodeFetched(ctx context.Context, contentType, body string) (image.Image, error) {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	if mt == "image/svg+xml" {
		w, h := svgSize(body)
		return document.DefaultDiagramRenderer{}.RenderDiagram(ctx, "svg", body, w, h)
	}
	return document.DecodeImage(strings.NewReader(body))
}

// decodeDataURL decodes a data: image locally (decision 12): SVG through the
// diagram renderer, raster formats through document.DecodeDataURL.
func decodeDataURL(ctx context.Context, src string) (image.Image, error) {
	meta, payload, ok := strings.Cut(strings.TrimPrefix(src, "data:"), ",")
	if !ok {
		return nil, errors.New("malformed data URL")
	}
	if strings.HasPrefix(strings.ToLower(meta), "image/svg+xml") {
		var raw []byte
		if strings.HasSuffix(strings.ToLower(meta), ";base64") {
			b, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				return nil, err
			}
			raw = b
		} else if unescaped, err := url.PathUnescape(payload); err == nil {
			raw = []byte(unescaped) // bundlers emit percent-encoded SVG payloads
		} else {
			raw = []byte(payload)
		}
		w, h := svgSize(string(raw))
		return document.DefaultDiagramRenderer{}.RenderDiagram(ctx, "svg", string(raw), w, h)
	}
	return document.DecodeDataURL(src)
}
