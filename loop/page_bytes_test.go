// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/nthtml/document"
)

// A DOM byte budget above the 1 MiB default raises nthtml's input cap with
// it, so a 2 MiB page renders; the host stylesheet allowance still fits
// beside it under nthtml's ceiling.
func TestPageByteBudgetRaisesRenderCap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: testProfile, Width: 400, MinFrame: time.Millisecond, DOMLimits: dom.Limits{TotalBytes: 4 << 20, ExternalStyleBytes: dom.MaxExternalStyleBytes}}, sink)
	if l.cfg.DOMLimits.TotalBytes != 4<<20 || l.cfg.RenderLimit.Bytes != 8<<20 || l.cfg.DOMLimits.ExternalStyleBytes != 4<<20 {
		t.Fatalf("budgets: dom %d render %d css %d", l.cfg.DOMLimits.TotalBytes, l.cfg.RenderLimit.Bytes, l.cfg.DOMLimits.ExternalStyleBytes)
	}
	go l.Run()
	var page strings.Builder
	page.WriteString("<body><div style=\"display:none\">") // 2 MiB of DOM that costs no layout
	for i := 0; i < 40; i++ {                              // 40 × 52 KiB of text in few nodes
		page.WriteString("<p>" + strings.Repeat("lorem ipsum ", 4500) + "</p>")
	}
	page.WriteString("</div><p id=end>end</p></body>")
	l.LoadPage(page.String(), "https://example.test/", nil)
	msg := waitFor(t, sink, func(m tea.Msg) bool {
		if lm, ok := m.(LogMsg); ok && lm.Level == "error" {
			return true
		}
		f, ok := m.(FrameMsg)
		return ok && (f.Err != nil || f.Result != nil)
	})
	switch v := msg.(type) {
	case LogMsg:
		t.Fatalf("2 MiB page within a 4 MiB budget failed: %s", v.Line)
	case FrameMsg:
		if v.Err != nil {
			t.Fatalf("2 MiB page within a 4 MiB budget failed: %v", v.Err)
		}
	}
	// The ceiling is nthtml's: a budget above it is clamped, and the
	// stylesheet allowance gives way to the DOM.
	l2 := New(ctx, Config{Profile: testProfile, Width: 400, DOMLimits: dom.Limits{TotalBytes: 64 << 20, ExternalStyleBytes: dom.MaxExternalStyleBytes}}, sink)
	if l2.cfg.DOMLimits.TotalBytes != document.MaxLimits.Bytes || l2.cfg.RenderLimit.Bytes != document.MaxLimits.Bytes || l2.cfg.DOMLimits.ExternalStyleBytes != 0 {
		t.Fatalf("clamp: dom %d render %d css %d", l2.cfg.DOMLimits.TotalBytes, l2.cfg.RenderLimit.Bytes, l2.cfg.DOMLimits.ExternalStyleBytes)
	}
}
