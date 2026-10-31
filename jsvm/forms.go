package jsvm

import (
	"strings"

	"github.com/dop251/goja"

	"nimbleterminal.dev/ntbrowser/dom"
)

// FormHooks are how form methods reach the host. They are called on the VM
// goroutine while script runs, so they must queue their work rather than
// re-enter the sandbox.
type FormHooks struct {
	Submit       func(form, submitter dom.NodeID, fireEvent bool) // submit(): no event; requestSubmit(): event
	Reset        func(form dom.NodeID)
	ValueChanged func(id dom.NodeID) // a script wrote a control's value, checkedness or selection
}

// SetFormHooks installs the host's form hooks (nil fields are ignored).
func (s *Sandbox) SetFormHooks(h FormHooks) { s.forms = h }

func (s *Sandbox) valueChanged(n *dom.Node) {
	if s.forms.ValueChanged != nil {
		s.forms.ValueChanged(n.ID())
	}
}

// installFormBindings adds the form-control surface to the shared element
// prototype; properties that do not apply to an element read as undefined.
func (s *Sandbox) installFormBindings(p *goja.Object) {
	vm := s.vm
	undef := goja.Undefined()
	isControl := func(n *dom.Node) bool {
		switch n.Tag() {
		case "input", "textarea", "select", "button":
			return true
		}
		return false
	}
	reflectBool := func(name, attr string, applies func(*dom.Node) bool) {
		s.accessor(p, name, func(t goja.Value) goja.Value {
			n := s.node(t)
			if !applies(n) {
				return undef
			}
			_, ok := n.Attr(attr)
			return vm.ToValue(ok)
		}, func(t, v goja.Value) {
			n := s.node(t)
			if v.ToBoolean() {
				s.underPressure(func() error { return s.tree.SetAttr(n, attr, "") })
			} else {
				s.tree.RemoveAttr(n, attr)
			}
		})
	}
	s.accessor(p, "value", func(t goja.Value) goja.Value {
		n := s.node(t)
		switch n.Tag() {
		case "input", "textarea", "select", "option", "button":
			return vm.ToValue(n.Value())
		}
		return undef
	}, func(t, v goja.Value) {
		n := s.node(t)
		val := v.String()
		before := n.Value()
		switch n.Tag() {
		case "input", "textarea":
			s.underPressure(func() error { return s.tree.SetValue(n, val) })
		case "select":
			var match *dom.Node
			for _, o := range dom.Options(n) {
				if dom.OptionValue(o) == val {
					match = o
					break
				}
			}
			for _, o := range dom.Options(n) {
				s.tree.SetSelected(o, o == match)
			}
		case "option", "button":
			s.underPressure(func() error { return s.tree.SetAttr(n, "value", val) })
			return
		default:
			return
		}
		if n.Value() != before { // input masks rewrite the same value on every keystroke
			s.valueChanged(n)
		}
	})
	s.accessor(p, "defaultValue", func(t goja.Value) goja.Value {
		n := s.node(t)
		switch n.Tag() {
		case "input":
			v, _ := n.Attr("value")
			return vm.ToValue(v)
		case "textarea":
			return vm.ToValue(dom.TextContent(n))
		}
		return undef
	}, func(t, v goja.Value) {
		n := s.node(t)
		switch n.Tag() {
		case "input":
			s.underPressure(func() error { return s.tree.SetAttr(n, "value", v.String()) })
		case "textarea":
			s.setText(t, v)
		}
	})
	isCheckable := func(n *dom.Node) bool {
		return n.Tag() == "input" && (dom.InputType(n) == "checkbox" || dom.InputType(n) == "radio")
	}
	s.accessor(p, "checked", func(t goja.Value) goja.Value {
		n := s.node(t)
		if n.Tag() != "input" {
			return undef
		}
		return vm.ToValue(n.Checked())
	}, func(t, v goja.Value) {
		n := s.node(t)
		if !isCheckable(n) {
			return
		}
		if v.ToBoolean() && dom.InputType(n) == "radio" {
			s.tree.CheckRadio(n)
		} else {
			s.tree.SetChecked(n, v.ToBoolean())
		}
		s.valueChanged(n)
	})
	reflectBool("defaultChecked", "checked", func(n *dom.Node) bool { return n.Tag() == "input" })
	s.accessor(p, "type", func(t goja.Value) goja.Value {
		n := s.node(t)
		switch n.Tag() {
		case "input":
			return vm.ToValue(dom.InputType(n))
		case "button":
			typ, _ := n.Attr("type")
			switch typ = strings.ToLower(strings.TrimSpace(typ)); typ {
			case "button", "reset":
				return vm.ToValue(typ)
			}
			return vm.ToValue("submit")
		case "select":
			if _, multi := n.Attr("multiple"); multi {
				return vm.ToValue("select-multiple")
			}
			return vm.ToValue("select-one")
		case "textarea":
			return vm.ToValue("textarea")
		}
		if v, ok := n.Attr("type"); ok {
			return vm.ToValue(v)
		}
		return undef
	}, func(t, v goja.Value) {
		n := s.node(t)
		s.underPressure(func() error { return s.tree.SetAttr(n, "type", v.String()) })
	})
	s.accessor(p, "name", func(t goja.Value) goja.Value {
		n := s.node(t)
		if !isControl(n) && n.Tag() != "form" {
			return undef
		}
		v, _ := n.Attr("name")
		return vm.ToValue(v)
	}, func(t, v goja.Value) {
		n := s.node(t)
		s.underPressure(func() error { return s.tree.SetAttr(n, "name", v.String()) })
	})
	reflectBool("disabled", "disabled", isControl)
	reflectBool("readOnly", "readonly", func(n *dom.Node) bool { return n.Tag() == "input" || n.Tag() == "textarea" })
	s.accessor(p, "form", func(t goja.Value) goja.Value {
		n := s.node(t)
		if !isControl(n) {
			return undef
		}
		if f := dom.FormOwner(n, s.tree); f != nil {
			return s.wrap(f)
		}
		return goja.Null()
	}, nil)
	s.accessor(p, "selectedIndex", func(t goja.Value) goja.Value {
		n := s.node(t)
		if n.Tag() != "select" {
			return undef
		}
		sel := dom.SelectedOptions(n)
		if len(sel) == 0 {
			return vm.ToValue(-1)
		}
		for i, o := range dom.Options(n) {
			if o == sel[0] {
				return vm.ToValue(i)
			}
		}
		return vm.ToValue(-1)
	}, func(t, v goja.Value) {
		n := s.node(t)
		if n.Tag() != "select" {
			return
		}
		idx := int(v.ToInteger())
		for i, o := range dom.Options(n) {
			s.tree.SetSelected(o, i == idx)
		}
		s.valueChanged(n)
	})
	s.accessor(p, "options", func(t goja.Value) goja.Value {
		n := s.node(t)
		if n.Tag() != "select" {
			return undef
		}
		return s.wrapAll(dom.Options(n), true)
	}, nil)
	s.accessor(p, "selected", func(t goja.Value) goja.Value {
		n := s.node(t)
		if n.Tag() != "option" {
			return undef
		}
		if sel := dom.OwnerSelect(n); sel != nil {
			for _, o := range dom.SelectedOptions(sel) {
				if o == n {
					return vm.ToValue(true)
				}
			}
			return vm.ToValue(false)
		}
		return vm.ToValue(n.Selected())
	}, func(t, v goja.Value) {
		n := s.node(t)
		if n.Tag() != "option" {
			return
		}
		if sel := dom.OwnerSelect(n); sel != nil {
			s.tree.SelectOption(sel, n, v.ToBoolean())
			s.valueChanged(sel)
			return
		}
		s.tree.SetSelected(n, v.ToBoolean())
	})
	s.accessor(p, "text", func(t goja.Value) goja.Value {
		n := s.node(t)
		if n.Tag() != "option" {
			return undef
		}
		return vm.ToValue(strings.Join(strings.Fields(dom.TextContent(n)), " "))
	}, nil)
	s.accessor(p, "elements", func(t goja.Value) goja.Value {
		n := s.node(t)
		if n.Tag() != "form" {
			return undef
		}
		controls := dom.FormControls(s.tree, n)
		arr := s.wrapAll(controls, true).(*goja.Object)
		_ = arr.Set("namedItem", func(c goja.FunctionCall) goja.Value {
			want := c.Argument(0).String()
			for _, m := range controls {
				if id, _ := m.Attr("id"); id == want {
					return s.wrap(m)
				}
				if name, _ := m.Attr("name"); name == want {
					return s.wrap(m)
				}
			}
			return goja.Null()
		})
		return arr
	}, nil)
	formThis := func(c goja.FunctionCall) *dom.Node {
		n := s.node(c.This)
		if n.Tag() != "form" {
			panic(vm.NewTypeError("Illegal invocation"))
		}
		return n
	}
	s.method(p, "submit", func(c goja.FunctionCall) goja.Value {
		if f := formThis(c); s.forms.Submit != nil {
			s.forms.Submit(f.ID(), 0, false)
		}
		return undef
	})
	s.method(p, "requestSubmit", func(c goja.FunctionCall) goja.Value {
		f := formThis(c)
		var submitter dom.NodeID
		if arg := c.Argument(0); !goja.IsUndefined(arg) && !goja.IsNull(arg) {
			b := s.nodeArg(arg)
			if !isSubmitter(b) || dom.FormOwner(b, s.tree) != f {
				panic(vm.NewTypeError("The specified element is not a submit button of this form"))
			}
			submitter = b.ID()
		}
		if s.forms.Submit != nil {
			s.forms.Submit(f.ID(), submitter, true)
		}
		return undef
	})
	s.method(p, "reset", func(c goja.FunctionCall) goja.Value {
		if f := formThis(c); s.forms.Reset != nil {
			s.forms.Reset(f.ID())
		}
		return undef
	})
}

func isSubmitter(n *dom.Node) bool {
	switch n.Tag() {
	case "button":
		typ, _ := n.Attr("type")
		typ = strings.ToLower(strings.TrimSpace(typ))
		return typ != "button" && typ != "reset"
	case "input":
		typ := dom.InputType(n)
		return typ == "submit" || typ == "image"
	}
	return false
}
