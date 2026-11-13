// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package compare

import (
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
)

func writeReport(dir string, r Report) error {
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(dir, "report.json"), append(b, '\n'), 0600); e != nil {
		return e
	}
	for name, text := range map[string]string{"glynx.txt": r.Glynx.Text, "chrome.txt": r.Chrome.Text} {
		if e = os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); e != nil {
			return e
		}
	}
	f, e := os.OpenFile(filepath.Join(dir, "index.html"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	e = reportTemplate.Execute(f, r)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}

var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Chrome / glynx comparison</title>
<style>body{font:16px system-ui;margin:24px;color:#222;background:#fafafa}h1{font-size:24px}.shots{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:20px;overflow:auto}#native:checked~.shots{display:flex}#native:checked~.shots img{width:auto}figure{margin:0;flex:none}img{border:1px solid #aaa;max-width:none;width:100%}figcaption{font-weight:bold;margin:12px 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:36em;overflow:auto}table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:8px;text-align:left}.error{color:#a00}details{margin:16px 0}summary{cursor:pointer}</style>
<h1>Chrome / glynx comparison</h1>
<p>{{.SourceURL}}</p><p>{{.Options.Width}} × {{.Options.Height}} CSS pixels · scale 1 · {{.Options.Scheme}} · scripts {{.Options.Scripts}} · settle {{.Options.Settle}}</p>
<p>{{.Note}}</p>
<p>Initial HTML SHA-256: <code>{{.SourceSHA256}}</code>. <a href="report.json">Full JSON</a> · <a href="glynx.txt">glynx text</a> · <a href="chrome.txt">Chrome text</a></p>
{{if .Glynx.Error}}<p class="error">glynx: {{.Glynx.Error}}</p>{{end}}{{if .Chrome.Error}}<p class="error">Chrome: {{.Chrome.Error}}</p>{{end}}
<p>Document bounds: glynx {{.Glynx.Width}} × {{.Glynx.Height}}; Chrome {{.Chrome.Width}} × {{.Chrome.Height}}. {{if and (not .Glynx.Error) (not .Chrome.Error)}}Exact pixel difference: {{printf "%.2f" .PixelDifferencePercent}}% (diagnostic only).{{else}}Pixel comparison unavailable.{{end}}</p>
<input id="native" type="checkbox"><label for="native"> Show images at native pixel size</label>
<div class="shots"><figure><figcaption>glynx loop</figcaption>{{if .Glynx.Screenshot}}<img src="glynx.png" alt="glynx viewport">{{end}}</figure><figure><figcaption>{{.Chrome.Engine}}</figcaption>{{if .Chrome.Screenshot}}<img src="chrome.png" alt="Chrome viewport">{{end}}</figure><figure><figcaption>Exact pixel differences</figcaption>{{if and .Glynx.Screenshot .Chrome.Screenshot}}<img src="diff.png" alt="Magenta pixel differences">{{end}}</figure></div>
<details open><summary>glynx warnings</summary><ul>{{range .Glynx.Warnings}}<li>{{.}}</li>{{else}}<li>No recorded warnings.</li>{{end}}</ul></details>
<details><summary>ID geometry (painted coverage versus border boxes)</summary><table><tr><th>ID</th><th>Observation</th><th>glynx</th><th>Chrome</th><th>Qualification</th></tr>{{range .Geometry}}<tr><td>{{.ID}}</td><td>{{.Status}}</td><td>{{.Glynx}}</td><td>{{.Chrome}}</td><td>{{.Note}}</td></tr>{{end}}</table></details>
<details><summary>glynx reading text</summary><pre>{{.Glynx.Text}}</pre></details><details><summary>Chrome visible text</summary><pre>{{.Chrome.Text}}</pre></details>
<p>Network and script events, raw layout boxes, Chrome computed font properties and build versions are in report.json. This capture does not exercise keyboard interaction or terminal image transport.</p></html>`))
