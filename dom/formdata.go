package dom

import (
	"net/url"
	"strings"
)

// FormEntry is one name/value pair of a form data set.
type FormEntry struct{ Name, Value string }

// FormControls lists the form's listed controls (button, input, select,
// textarea) in tree order, including those associated through the form
// attribute.
func FormControls(t *Tree, form *Node) []*Node {
	var out []*Node
	Walk(t.root, func(n *Node) {
		if n.kind == Element && isFormControl(n.tag) && FormOwner(n, t) == form {
			out = append(out, n)
		}
	})
	return out
}

func isSubmitButton(n *Node) bool {
	switch n.tag {
	case "button":
		typ, _ := n.Attr("type")
		typ = strings.ToLower(strings.TrimSpace(typ))
		return typ != "button" && typ != "reset"
	case "input":
		typ := InputType(n)
		return typ == "submit" || typ == "image"
	}
	return false
}

// FormDataSet builds HTML's entry list for form submitted by submitter (nil
// for none): enabled named controls in tree order; checked boxes and radios
// (value default "on"); selected enabled options; the submitter alone among
// buttons; textarea line breaks normalized to CRLF. File and image inputs
// contribute nothing in this round.
func FormDataSet(t *Tree, form, submitter *Node) []FormEntry {
	var out []FormEntry
	for _, n := range FormControls(t, form) {
		name, _ := n.Attr("name")
		if name == "" || Disabled(n) {
			continue
		}
		switch {
		case n.tag == "button" || (n.tag == "input" && isButtonType(InputType(n))):
			if n == submitter && isSubmitButton(n) && InputType(n) != "image" {
				v, _ := n.Attr("value")
				out = append(out, FormEntry{name, v})
			}
		case n.tag == "select":
			for _, o := range SelectedOptions(n) {
				if !OptionDisabled(o) {
					out = append(out, FormEntry{name, OptionValue(o)})
				}
			}
		case n.tag == "textarea":
			out = append(out, FormEntry{name, crlf(n.Value())})
		default: // input
			switch InputType(n) {
			case "checkbox", "radio":
				if n.Checked() {
					v, ok := n.Attr("value")
					if !ok {
						v = "on"
					}
					out = append(out, FormEntry{name, v})
				}
			case "file":
			default:
				out = append(out, FormEntry{name, n.Value()})
			}
		}
	}
	return out
}

func isButtonType(typ string) bool {
	switch typ {
	case "submit", "reset", "button", "image":
		return true
	}
	return false
}

func crlf(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// DefaultButton is the form's first submit button in tree order, or nil.
func DefaultButton(t *Tree, form *Node) *Node {
	for _, n := range FormControls(t, form) {
		if isSubmitButton(n) {
			return n
		}
	}
	return nil
}

// BlocksImplicitSubmission reports the input kinds that count toward HTML's
// "only one field" rule for Enter without a submit button.
func BlocksImplicitSubmission(n *Node) bool { return n.tag == "input" && IsTextControl(n) }

// Encode serializes entries as application/x-www-form-urlencoded (UTF-8).
func Encode(entries []FormEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = url.QueryEscape(e.Name) + "=" + url.QueryEscape(e.Value)
	}
	return strings.Join(parts, "&")
}
