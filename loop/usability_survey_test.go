// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/subres"
)

type surveyCase struct {
	ID, Source, HTML string
	CSS              []string
	Scripts          []struct{ Path, Code, Type string }
}
type surveyManifest struct {
	Cases  []surveyCase
	SHA256 map[string]string
}

const surveyRoot = "../internal/usability/corpus"

func surveyRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(surveyRoot, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func surveyLoad(t *testing.T) surveyManifest {
	t.Helper()
	var m surveyManifest
	if err := json.Unmarshal(surveyRead(t, "manifest.json"), &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestUsabilityCorpusIntegrity(t *testing.T) {
	m := surveyLoad(t)
	if len(m.Cases) != 30 {
		t.Fatalf("want 30 fixtures, got %d", len(m.Cases))
	}
	for name, want := range m.SHA256 {
		if got := fmt.Sprintf("%x", sha256.Sum256(surveyRead(t, name))); got != want {
			t.Errorf("%s hash mismatch", name)
		}
	}
	seen := map[string]bool{}
	for _, c := range m.Cases {
		if seen[c.ID] {
			t.Errorf("duplicate %s", c.ID)
		}
		seen[c.ID] = true
		refs := append([]string{c.HTML}, c.CSS...)
		for _, s := range c.Scripts {
			if s.Path != "" {
				refs = append(refs, s.Path)
			}
		}
		for _, ref := range refs {
			if _, ok := m.SHA256[ref]; !ok {
				t.Errorf("unhashed reference %s", ref)
			}
		}
	}
}

type surveyObservation struct {
	Warnings             []string
	ID                   string
	Width                int
	Scripts              bool
	RenderError          string `json:",omitempty"`
	Text                 string
	Boxes                int
	FocusOrder           []string
	ActivationChangedDOM bool
	Logs                 []LogMsg
}

// Opt-in diagnostic survey, not an assertion that an upstream widget is usable.
// Only the local fixture server is admitted. Script bytes are embedded.
func TestUsabilitySurvey(t *testing.T) {
	output := os.Getenv("NTBROWSER_USABILITY_REPORT")
	if output == "" {
		t.Skip("set NTBROWSER_USABILITY_REPORT to write the local survey")
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(surveyRoot)))
	defer srv.Close()
	origin, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var results []surveyObservation
	for _, c := range surveyLoad(t).Cases {
		for _, width := range []int{320, 800} {
			for _, scripts := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/scripts=%t", c.ID, width, scripts), func(t *testing.T) {
					o := surveyObservation{ID: c.ID, Width: width, Scripts: scripts}
					var html strings.Builder
					html.WriteString("<!doctype html><html lang=en><head><meta charset=utf-8>")
					for _, css := range c.CSS {
						html.WriteString("<link rel=stylesheet href=\"" + srv.URL + "/" + css + "\">")
					}
					html.WriteString("</head><body>")
					html.Write(surveyRead(t, c.HTML))
					for _, s := range c.Scripts {
						html.WriteString("<script type=\"")
						html.WriteString(s.Type)
						html.WriteString("\">")
						if s.Path != "" {
							html.Write(surveyRead(t, s.Path))
						} else {
							html.WriteString(s.Code)
						}
						html.WriteString("</script>")
					}
					html.WriteString("</body></html>")
					p, _ := profile.Builtin("locked")
					p.Subresources = profile.SubOrigin
					if scripts {
						p.Scripts = profile.ScriptsInline
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					sink := make(chanSink, 4096)
					l := New(ctx, Config{Profile: p, Width: width, MinFrame: time.Millisecond, Subresources: subres.Config{Mode: subres.ModeOrigin}, SubresourceTransport: func(string) subres.Transport {
						return netpolicy.New(netpolicy.Policy{Origin: origin, AllowPrivate: true})
					}}, sink)
					go l.Run()
					l.LoadPage(html.String(), srv.URL+"/"+c.ID, netpolicy.New(netpolicy.Policy{Origin: origin, AllowPrivate: true}))
					deadline := time.NewTimer(10 * time.Second)
					defer deadline.Stop()
					ready := false
					for !ready {
						select {
						case <-deadline.C:
							o.RenderError = "initial frame timeout"
							ready = true
						case msg := <-sink:
							switch m := msg.(type) {
							case LogMsg:
								o.Logs = append(o.Logs, m)
								if strings.HasPrefix(m.Line, "parse:") {
									o.RenderError = m.Line
									ready = true
								}
							case FrameMsg:
								if m.Err != nil {
									o.RenderError = m.Err.Error()
									ready = true
								} else if m.Result != nil {
									o.Boxes = len(m.Result.Boxes)
									o.Warnings = append(m.Warnings, m.Result.Warnings...)
									text, err := m.Result.TTY(ctx, width/10)
									o.Text = text
									if err != nil {
										o.RenderError = err.Error()
									}
									ready = true
								}
							}
						}
					}
					if o.RenderError == "" {
						barrier := func(fn func()) {
							done := make(chan struct{})
							l.post(func() { fn(); close(done) })
							select {
							case <-done:
							case <-ctx.Done():
								t.Fatal(ctx.Err())
							case <-time.After(5 * time.Second):
								t.Fatal("owner barrier timed out")
							}
						}
						var before string
						barrier(func() {
							for _, n := range l.focusOrder(l.page) {
								id, _ := n.Attr("id")
								o.FocusOrder = append(o.FocusOrder, fmt.Sprintf("%s:%s", n.Tag(), id))
							}
							before = l.page.Tree.InnerHTML(l.page.Tree.Root())
						})
						if len(o.FocusOrder) > 0 {
							l.FocusNext(false)
							l.Activate()
							barrier(func() { o.ActivationChangedDOM = before != l.page.Tree.InnerHTML(l.page.Tree.Root()) })
						}
					}
				drain:
					for {
						select {
						case msg := <-sink:
							if m, ok := msg.(LogMsg); ok {
								o.Logs = append(o.Logs, m)
							}
						default:
							break drain
						}
					}
					for i := range o.Logs {
						o.Logs[i].Line = strings.ReplaceAll(o.Logs[i].Line, srv.URL, "http://fixture.local")
					}
					for i := range o.Warnings {
						o.Warnings[i] = strings.ReplaceAll(o.Warnings[i], srv.URL, "http://fixture.local")
					}
					results = append(results, o)
				})
			}
		}
	}
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, append(b, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
