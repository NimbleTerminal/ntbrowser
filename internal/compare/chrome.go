// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package compare

// Pipe-based CDP capture, adapted from nthtml/internal/conformance/differential.go.
// A fresh Chrome profile is used; no debugging port or existing user session.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type cdpMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}
type cdp struct {
	cmd          *exec.Cmd
	in, out      *os.File
	dir, session string
	next         int
	messages     chan cdpMessage
	errors       chan error
	cancel       context.CancelFunc
	initial      Source
	supplied     bool
	load         bool
	events       []Event
	dropped      int
}

func findChrome(explicit string) (string, error) {
	if explicit != "" {
		return exec.LookPath(explicit)
	}
	if p := os.Getenv("CHROME_BIN"); p != "" {
		return exec.LookPath(p)
	}
	for _, p := range []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium", "chromium", "chromium-browser", "google-chrome", "chrome-headless-shell"} {
		if v, e := exec.LookPath(p); e == nil {
			return v, nil
		}
	}
	return "", errors.New("chrome not found; set --chrome or CHROME_BIN")
}
func startChrome(ctx context.Context, executable string, src Source) (*cdp, error) {
	bin, err := findChrome(executable)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "glynx-compare-chrome-")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &cdp{dir: dir, cancel: cancel, initial: src, messages: make(chan cdpMessage, 128), errors: make(chan error, 1)}
	inR, inW, err := os.Pipe()
	if err != nil {
		c.close()
		return nil, err
	}
	c.in = inW
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		c.close()
		return nil, err
	}
	c.out = outR
	c.cmd = exec.CommandContext(ctx, bin, "--headless", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--disable-extensions", "--disable-background-networking", "--disable-sync", "--disable-breakpad", "--hide-scrollbars", "--mute-audio", "--user-data-dir="+dir, "--remote-debugging-pipe", "about:blank")
	c.cmd.ExtraFiles = []*os.File{inR, outW}
	err = c.cmd.Start()
	_ = inR.Close()
	_ = outW.Close()
	if err != nil {
		c.close()
		return nil, err
	}
	go func() {
		r := bufio.NewReaderSize(outR, 64<<10)
		for {
			var raw []byte
			for {
				part, e := r.ReadSlice(0)
				if len(raw)+len(part) > 64<<20 {
					c.errors <- errors.New("CDP message exceeds 64 MiB")
					return
				}
				raw = append(raw, part...)
				if e == nil {
					break
				}
				if e != bufio.ErrBufferFull {
					select {
					case c.errors <- e:
					case <-ctx.Done():
					}
					return
				}
			}
			var m cdpMessage
			if e := json.Unmarshal(bytes.TrimSuffix(raw, []byte{0}), &m); e != nil {
				c.errors <- e
				return
			}
			select {
			case c.messages <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	return c, nil
}
func (c *cdp) close() {
	c.cancel()
	if c.in != nil {
		_ = c.in.Close()
	}
	if c.out != nil {
		_ = c.out.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Wait()
	}
	if c.dir != "" {
		_ = os.RemoveAll(c.dir)
	}
}
func (c *cdp) send(method string, params any, browser bool) (int, error) {
	c.next++
	p := map[string]any{"id": c.next, "method": method, "params": params}
	if !browser && c.session != "" {
		p["sessionId"] = c.session
	}
	b, e := json.Marshal(p)
	if e != nil {
		return 0, e
	}
	_, e = c.in.Write(append(b, 0))
	return c.next, e
}
func (c *cdp) event(m cdpMessage) error {
	if m.Method == "Page.loadEventFired" {
		c.load = true
	}
	if m.Method == "Fetch.requestPaused" {
		var p struct {
			RequestID    string
			ResourceType string
			Request      struct{ URL, Method string }
		}
		if e := json.Unmarshal(m.Params, &p); e != nil {
			return e
		}
		if !c.supplied && p.ResourceType == "Document" && p.Request.URL == c.initial.URL {
			c.supplied = true
			headers := []map[string]string{{"name": "Content-Type", "value": c.initial.ContentType}}
			for _, key := range []string{"Content-Security-Policy", "X-Frame-Options"} {
				if value := c.initial.Headers[strings.ToLower(key)]; value != "" {
					headers = append(headers, map[string]string{"name": key, "value": value})
				}
			}
			_, e := c.send("Fetch.fulfillRequest", map[string]any{"requestId": p.RequestID, "responseCode": c.initial.Status, "responseHeaders": headers, "body": base64.StdEncoding.EncodeToString([]byte(c.initial.HTML))}, false)
			return e
		}
		_, e := c.send("Fetch.continueRequest", map[string]any{"requestId": p.RequestID}, false)
		return e
	}
	switch m.Method {
	case "Runtime.exceptionThrown", "Log.entryAdded", "Network.loadingFailed", "Network.responseReceived":
		if len(c.events) < 2048 && len(m.Params) < 64<<10 {
			c.events = append(c.events, Event{Kind: m.Method, Data: append(json.RawMessage(nil), m.Params...)})
		} else {
			c.dropped++
		}

	}
	return nil
}
func (c *cdp) nextMessage(ctx context.Context) (cdpMessage, error) {
	select {
	case <-ctx.Done():
		return cdpMessage{}, ctx.Err()
	case e := <-c.errors:
		return cdpMessage{}, e
	case m := <-c.messages:
		return m, c.event(m)
	}
}
func (c *cdp) call(ctx context.Context, method string, params any, browser bool) (json.RawMessage, error) {
	id, e := c.send(method, params, browser)
	if e != nil {
		return nil, e
	}
	for {
		m, e := c.nextMessage(ctx)
		if e != nil {
			return nil, e
		}
		if m.ID == id {
			if m.Error != nil {
				return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
			}
			return m.Result, nil
		}
	}
}
func (c *cdp) wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case e := <-c.errors:
			return e
		case m := <-c.messages:
			if e := c.event(m); e != nil {
				return e
			}
		}
	}
}
func captureChrome(ctx context.Context, o Options, src Source) (Capture, error) {
	c, e := startChrome(ctx, o.Chrome, src)
	if e != nil {
		return Capture{}, e
	}
	defer c.close()
	call := func(method string, p any) (json.RawMessage, error) { return c.call(ctx, method, p, false) }
	raw, e := c.call(ctx, "Browser.getVersion", map[string]any{}, true)
	if e != nil {
		return Capture{}, e
	}
	var version struct{ Product string }
	if e = json.Unmarshal(raw, &version); e != nil {
		return Capture{}, e
	}
	raw, e = c.call(ctx, "Target.createTarget", map[string]any{"url": "about:blank"}, true)
	if e != nil {
		return Capture{}, e
	}
	var target struct{ TargetID string }
	if e = json.Unmarshal(raw, &target); e != nil {
		return Capture{}, e
	}
	raw, e = c.call(ctx, "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, true)
	if e != nil {
		return Capture{}, e
	}
	var attach struct{ SessionID string }
	if e = json.Unmarshal(raw, &attach); e != nil {
		return Capture{}, e
	}
	c.session = attach.SessionID
	commands := []struct {
		method string
		params any
	}{
		{"Page.enable", map[string]any{}}, {"Runtime.enable", map[string]any{}}, {"Network.enable", map[string]any{}}, {"Log.enable", map[string]any{}},
		{"Network.setCacheDisabled", map[string]any{"cacheDisabled": true}},
		{"Emulation.setDeviceMetricsOverride", map[string]any{"width": o.Width, "height": o.Height, "deviceScaleFactor": 1, "mobile": false}},
		{"Emulation.setEmulatedMedia", map[string]any{"media": "screen", "features": []map[string]string{{"name": "prefers-color-scheme", "value": o.Scheme}, {"name": "prefers-reduced-motion", "value": "reduce"}}}},
		{"Emulation.setScriptExecutionDisabled", map[string]any{"value": !o.Scripts}},
		{"Fetch.enable", map[string]any{"patterns": []map[string]string{{"urlPattern": src.URL, "resourceType": "Document"}}}},
		{"Page.navigate", map[string]any{"url": src.URL}},
	}
	for _, cmd := range commands {
		if cmd.method == "Page.navigate" {
			c.load = false
		}
		if _, e = call(cmd.method, cmd.params); e != nil {
			return Capture{}, e
		}
	}
	for !c.load {
		if _, e = c.nextMessage(ctx); e != nil {
			return Capture{}, e
		}
	}
	if e = c.wait(ctx, o.Settle); e != nil {
		return Capture{}, e
	}
	// Isolated-world measurement avoids application replacements of DOM globals.
	raw, e = call("Page.getFrameTree", map[string]any{})
	if e != nil {
		return Capture{}, e
	}
	var tree struct {
		FrameTree struct{ Frame struct{ ID string } }
	}
	if e = json.Unmarshal(raw, &tree); e != nil {
		return Capture{}, e
	}
	raw, e = call("Page.createIsolatedWorld", map[string]any{"frameId": tree.FrameTree.Frame.ID, "worldName": "glynx-comparison"})
	if e != nil {
		return Capture{}, e
	}
	var world struct{ ExecutionContextID int }
	if e = json.Unmarshal(raw, &world); e != nil {
		return Capture{}, e
	}
	expr := `(async()=>{await Promise.race([document.fonts.ready,new Promise(r=>setTimeout(r,2000))]);const root=document.documentElement;const nodes=[...document.querySelectorAll('[id],h1,h2,h3,header,nav,main,article,pre')].slice(0,512);return {url:location.href,width:root.scrollWidth,height:root.scrollHeight,text:(document.body?.innerText||'').slice(0,1048576),elements:nodes.map(el=>{const r=el.getBoundingClientRect(),s=getComputedStyle(el);return {id:el.id,tag:el.localName,text:(el.innerText||'').slice(0,160),rect:{x:r.x,y:r.y,width:r.width,height:r.height},display:s.display,font:s.fontFamily,fontSize:s.fontSize,lineHeight:s.lineHeight}})}})()`
	raw, e = call("Runtime.evaluate", map[string]any{"expression": expr, "contextId": world.ExecutionContextID, "awaitPromise": true, "returnByValue": true})
	if e != nil {
		return Capture{}, e
	}
	var evaluated struct {
		Result           struct{ Value Capture }
		ExceptionDetails json.RawMessage
	}
	if e = json.Unmarshal(raw, &evaluated); e != nil {
		return Capture{}, e
	}
	if len(evaluated.ExceptionDetails) > 0 {
		return Capture{}, errors.New("chrome measurement threw; see page diagnostics")
	}
	result := evaluated.Result.Value
	result.Engine = version.Product
	raw, e = call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false})
	if e != nil {
		return result, e
	}
	var shot struct{ Data string }
	if e = json.Unmarshal(raw, &shot); e != nil {
		return result, e
	}
	png, e := base64.StdEncoding.DecodeString(shot.Data)
	if e != nil {
		return result, e
	}
	if !c.supplied {
		return result, errors.New("chrome did not consume the shared initial HTML")
	}
	result.Events = c.events
	result.DroppedEvents = c.dropped
	result.Screenshot = "chrome.png"
	return result, os.WriteFile(filepath.Join(o.Out, result.Screenshot), png, 0600)
}
