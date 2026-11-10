// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package netpolicy

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"nimbleterminal.dev/ntbrowser/origin"
)

type integrityMetadata struct {
	strength int
	digests  [][]byte
}

func parseIntegrity(raw string) (integrityMetadata, error) {
	var out integrityMetadata
	deny := func() (integrityMetadata, error) {
		return out, fmt.Errorf("%w: invalid or unsupported script integrity metadata", ErrDenied)
	}
	if len(raw) > 4096 {
		return deny()
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\f' })
	if len(fields) > 32 {
		return deny()
	}
	for _, token := range fields {
		algorithm, encoded, ok := strings.Cut(token, "-")
		if !ok {
			return deny()
		}
		strength := 0
		switch strings.ToLower(algorithm) {
		case "sha256":
			strength = 32
		case "sha384":
			strength = 48
		case "sha512":
			strength = 64
		default:
			continue
		}
		// No integrity options are implemented. Malformed recognized algorithms
		// fail closed rather than falling back to a weaker digest.
		if strings.ContainsAny(encoded, "? \t\r\n\f") {
			return deny()
		}
		encoded = strings.NewReplacer("-", "+", "_", "/").Replace(encoded)
		digest, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			digest, err = base64.RawStdEncoding.DecodeString(encoded)
		}
		if err != nil || len(digest) != strength {
			return deny()
		}
		if strength > out.strength {
			out.strength = strength
			out.digests = nil
		}
		if strength == out.strength {
			out.digests = append(out.digests, digest)
		}
	}
	if raw != "" && out.strength == 0 {
		return deny()
	}
	return out, nil
}
func (m integrityMetadata) verify(body string) error {
	if m.strength == 0 {
		return nil
	}
	var digest []byte
	switch m.strength {
	case 32:
		h := sha256.Sum256([]byte(body))
		digest = h[:]
	case 48:
		h := sha512.Sum384([]byte(body))
		digest = h[:]
	case 64:
		h := sha512.Sum512([]byte(body))
		digest = h[:]
	}
	for _, expected := range m.digests {
		if subtle.ConstantTimeCompare(digest, expected) == 1 {
			return nil
		}
	}
	return fmt.Errorf("%w: script integrity mismatch", ErrDenied)
}

type scriptIntegrityKey struct{}
type scriptIntegrityContext struct {
	origin *url.URL
	cors   bool
}

func (s *scriptIntegrityContext) checkResponse(u *url.URL, h http.Header) error {
	if origin.Same(s.origin, u) {
		return nil
	}
	values := h.Values("Access-Control-Allow-Origin")
	own, ok := origin.OfURL(s.origin)
	if !s.cors || len(values) != 1 || (values[0] != "*" && !(ok && values[0] == own)) {
		return fmt.Errorf("%w: integrity script requires an anonymous CORS response", ErrDenied)
	}
	return nil
}

// Script verifies the exact decoded response bytes before returning executable
// source. Neither integrity metadata nor CORS grants any network authority.
// corsMode is "none" when the attribute is absent, otherwise its reflected value.
func (f *Fetcher) Script(ctx context.Context, src, integrity, corsMode string) (Response, error) {
	metadata, err := parseIntegrity(integrity)
	if err != nil {
		return Response{}, err
	}
	if metadata.strength != 0 {
		if f.p.Origin == nil || (f.p.Origin.Scheme != "http" && f.p.Origin.Scheme != "https") {
			return Response{}, fmt.Errorf("%w: integrity script has no HTTP origin", ErrDenied)
		}
		if strings.EqualFold(corsMode, "use-credentials") {
			return Response{}, fmt.Errorf("%w: credentialed integrity scripts unsupported", ErrDenied)
		}
		state := &scriptIntegrityContext{origin: f.p.Origin, cors: corsMode != "none"}
		target, err := f.Resolve(src)
		if err != nil {
			return Response{}, err
		}
		if !origin.Same(f.p.Origin, target) && !state.cors {
			return Response{}, fmt.Errorf("%w: cross-origin integrity script requires crossorigin", ErrDenied)
		}
		ctx = context.WithValue(ctx, scriptIntegrityKey{}, state)
	}
	response, err := f.Do(ctx, Request{Method: "GET", URL: src, Kind: KindScript})
	if err != nil {
		return Response{}, err
	}
	if err := metadata.verify(response.Body); err != nil {
		return Response{}, err
	}
	return response, nil
}
