// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package compare

import (
	"context"
	"errors"
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/nthtml"
	"nimbleterminal.dev/nthtml/document"
)

func TestCaptureViewportFollowsReplacement(t *testing.T) {
	for _, notificationFirst := range []bool{false, true} {
		name := "cancellation before frame"
		if notificationFirst {
			name = "frame before cancellation"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), patience)
			defer cancel()
			old := loop.FrameMsg{Result: new(document.Result), Gen: 1}
			next := loop.FrameMsg{Result: new(document.Result), Gen: 2, Painted: new(nthtml.PaintedResult)}
			messages := make(chan tea.Msg, 4)
			want := image.NewRGBA(image.Rect(0, 0, 1, 1))
			calls := 0
			frame, raster, err := captureViewport(ctx, testOptions(t), old, messages, func(req nthtml.PaintRequest) {
				calls++
				if calls == 1 {
					canceled := nthtml.PaintReady{Request: req, Err: context.Canceled}
					if notificationFirst {
						messages <- next
						messages <- canceled
					} else {
						messages <- canceled
						messages <- next
					}
				} else {
					if req.Result != next.Result || req.Revision != next.Gen {
						t.Errorf("wrong replacement request: %+v", req)
					}
					messages <- nthtml.PaintReady{Request: req, Image: want}
				}
			})
			if err != nil || frame.Gen != 2 || raster != want || calls != 2 {
				t.Fatalf("frame=%d raster=%v calls=%d err=%v", frame.Gen, raster, calls, err)
			}
		})
	}
}

func TestCaptureViewportPreservesErrorsAndCallerCancellation(t *testing.T) {
	for _, callerCanceled := range []bool{false, true} {
		name := "paint deadline"
		if callerCanceled {
			name = "caller canceled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), patience)
			defer cancel()
			messages := make(chan tea.Msg, 1)
			want := error(context.DeadlineExceeded)
			if callerCanceled {
				want = context.Canceled
			}
			_, _, err := captureViewport(ctx, testOptions(t), loop.FrameMsg{Result: new(document.Result), Gen: 1}, messages, func(req nthtml.PaintRequest) {
				if callerCanceled {
					cancel()
				}
				messages <- nthtml.PaintReady{Request: req, Err: want}
			})
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}
