// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"strconv"
	"strings"
)

type StyleDeclaration struct{ Name, Value, Priority string }

// SupportedStyleProperties bounds the reflected CSSStyleDeclaration surface.
// Declaration values are still interpreted by nthtml's CSS renderer.
var SupportedStyleProperties = strings.Fields(`align-content align-items align-self aspect-ratio background background-color background-image background-position background-position-x background-position-y background-repeat background-size border border-bottom border-bottom-color border-bottom-style border-bottom-width border-collapse border-color border-left border-left-color border-left-style border-left-width border-radius border-right border-right-color border-right-style border-right-width border-spacing border-style border-top border-top-color border-top-style border-top-width border-width bottom box-sizing caption-side clear color column-gap cursor direction display flex flex-basis flex-direction flex-flow flex-grow flex-shrink flex-wrap float font font-family font-size font-style font-variant font-weight gap height inset justify-content left letter-spacing line-height list-style list-style-image list-style-position list-style-type margin margin-bottom margin-left margin-right margin-top max-height max-width min-height min-width object-fit object-position opacity order outline outline-color outline-style outline-width overflow overflow-wrap overflow-x overflow-y padding padding-bottom padding-left padding-right padding-top pointer-events position right row-gap table-layout text-align text-decoration text-indent text-overflow text-transform top vertical-align visibility white-space width word-break word-spacing word-wrap z-index`)
var styleProperties = func() map[string]bool {
	m := map[string]bool{}
	for _, p := range SupportedStyleProperties {
		m[p] = true
	}
	return m
}()

func StyleProperty(name string) bool {
	return styleProperties[name] || strings.HasPrefix(name, "--") && len(name) > 2 && validName("x"+name)
}
func StyleName(name string) string {
	if strings.HasPrefix(name, "--") {
		return name
	}
	return strings.ToLower(name)
}

// cssParts recognizes separators only outside strings, comments and balanced
// component values. Fixed input/DOM limits bound this non-preemptible scan.
func cssParts(source string, separator byte) (parts []string, valid bool) {
	var b strings.Builder
	var stack []byte
	var quote byte
	escaped := false
	for i := 0; i < len(source); i++ {
		c := source[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			b.WriteByte(c)
			escaped = true
			continue
		}
		if quote != 0 {
			b.WriteByte(c)
			if c == quote {
				quote = 0
			}
			if c == '\n' || c == '\r' {
				return parts, false
			}
			continue
		}
		if c == '/' && i+1 < len(source) && source[i+1] == '*' {
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return parts, false
			}
			i += end + 3
			b.WriteByte(' ')
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			b.WriteByte(c)
			continue
		}
		switch c {
		case '(', '[', '{':
			if len(stack) >= 64 {
				return parts, false
			}
			stack = append(stack, c)
		case ')', ']', '}':
			if len(stack) == 0 {
				return parts, false
			}
			open := stack[len(stack)-1]
			if c == ')' && open != '(' || c == ']' && open != '[' || c == '}' && open != '{' {
				return parts, false
			}
			stack = stack[:len(stack)-1]
		}
		if c == separator && len(stack) == 0 {
			parts = append(parts, b.String())
			b.Reset()
		} else {
			b.WriteByte(c)
		}
	}
	parts = append(parts, b.String())
	return parts, quote == 0 && len(stack) == 0 && !escaped
}
func styleValue(name, value string) bool {
	if value == "" {
		return false
	}
	parts, ok := cssParts(value, ';')
	if !ok || len(parts) != 1 {
		return false
	}
	if !strings.HasPrefix(name, "--") && strings.ContainsAny(value, "{}") {
		return false
	}
	bangs, ok := cssParts(value, '!')
	if !ok || len(bangs) != 1 {
		return false
	}
	if strings.HasPrefix(name, "--") {
		return true
	}
	v := strings.ToLower(value)
	switch v {
	case "inherit", "initial", "unset", "revert", "revert-layer":
		return true
	}
	if strings.Contains(v, "(") {
		return true
	} // bounded component syntax; renderer validates functions.
	switch name {
	case "width", "height", "min-width", "min-height", "max-width", "max-height", "top", "right", "bottom", "left":
		switch v {
		case "auto", "none", "min-content", "max-content", "fit-content", "stretch":
			return true
		}
		unit := ""
		number := v
		for _, suffix := range []string{"vmin", "vmax", "rem", "px", "em", "%", "vh", "vw", "ch", "ex", "pt", "pc", "cm", "mm", "in"} {
			if strings.HasSuffix(v, suffix) {
				unit, number = suffix, strings.TrimSuffix(v, suffix)
				break
			}
		}
		n, e := strconv.ParseFloat(number, 64)
		if e != nil || strings.ContainsAny(number, "nNiI") {
			return false
		}
		if unit == "" {
			return n == 0
		}
		switch unit {
		case "px", "em", "rem", "%", "vh", "vw", "vmin", "vmax", "ch", "ex", "pt", "pc", "cm", "mm", "in":
			return true
		}
		return false
	case "display":
		return strings.Contains("|none|block|inline|inline-block|flex|inline-flex|grid|inline-grid|table|table-row|table-cell|table-row-group|table-header-group|table-footer-group|table-caption|list-item|contents|flow-root|", "|"+v+"|")
	}
	return true
}
func ParseInlineStyle(source string) []StyleDeclaration {
	if len(source) > MaxFragmentBytes {
		return nil
	}
	parts, _ := cssParts(source, ';')
	var out []StyleDeclaration
	for _, part := range parts {
		name, val, ok := strings.Cut(part, ":")
		name = StyleName(strings.TrimSpace(name))
		if !ok || !StyleProperty(name) {
			continue
		}
		priority := ""
		bangs, valid := cssParts(val, '!')
		if !valid {
			continue
		}
		if len(bangs) == 2 && strings.EqualFold(strings.TrimSpace(bangs[1]), "important") {
			priority = "important"
			val = bangs[0]
		} else if len(bangs) != 1 {
			continue
		}
		val = strings.TrimSpace(val)
		if !styleValue(name, val) {
			continue
		}
		found := -1
		for i, d := range out {
			if d.Name == name {
				found = i
				break
			}
		}
		d := StyleDeclaration{name, val, priority}
		if found >= 0 {
			if out[found].Priority == "" || priority != "" {
				out[found] = d
			}
		} else {
			out = append(out, d)
		}
	}
	return out
}
func SerializeInlineStyle(decls []StyleDeclaration) string {
	var b strings.Builder
	for i, d := range decls {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(d.Name)
		b.WriteString(": ")
		b.WriteString(d.Value)
		if d.Priority != "" {
			b.WriteString(" !important")
		}
		b.WriteByte(';')
	}
	return b.String()
}
func SetInlineProperty(source, name, value, priority string) (string, bool) {
	name = StyleName(name)
	if !StyleProperty(name) {
		return source, false
	}
	declarations := ParseInlineStyle(source)
	value = strings.TrimSpace(value)
	if value != "" {
		if priority != "" && !strings.EqualFold(priority, "important") {
			return source, false
		}
		if !styleValue(name, value) {
			return source, false
		}
	}
	out := make([]StyleDeclaration, 0, len(declarations)+1)
	found := false
	for _, d := range declarations {
		if d.Name != name {
			out = append(out, d)
			continue
		}
		found = true
		if value != "" {
			out = append(out, StyleDeclaration{name, value, strings.ToLower(priority)})
		}
	}
	if value != "" && !found {
		out = append(out, StyleDeclaration{name, value, strings.ToLower(priority)})
	}
	return SerializeInlineStyle(out), true
}
