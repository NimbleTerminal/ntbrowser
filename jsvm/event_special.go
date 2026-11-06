// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import "github.com/dop251/goja"

// Constructor dictionaries describe synthetic event data. They do not create
// device producers or confer a trusted reader activation.
func (s *Sandbox) installSpecialEvents() {
	protos := map[string]*goja.Object{"Event": s.eventProto, "CustomEvent": s.customEventProto}
	s.specialEventProtos = protos
	s.specialEventDefaults = map[*goja.Object]map[string]any{}
	type definition struct {
		name, parent string
		fields       map[string]any
	}
	definitions := []definition{
		{"UIEvent", "Event", map[string]any{"view": nil, "detail": 0}},
		{"MouseEvent", "UIEvent", map[string]any{"screenX": 0, "screenY": 0, "clientX": 0, "clientY": 0, "button": 0, "buttons": 0, "ctrlKey": false, "shiftKey": false, "altKey": false, "metaKey": false, "relatedTarget": nil}},
		{"KeyboardEvent", "UIEvent", map[string]any{"key": "", "code": "", "location": 0, "repeat": false, "isComposing": false, "ctrlKey": false, "shiftKey": false, "altKey": false, "metaKey": false, "charCode": 0, "keyCode": 0, "which": 0}},
		{"FocusEvent", "UIEvent", map[string]any{"relatedTarget": nil}},
		{"InputEvent", "UIEvent", map[string]any{"data": nil, "inputType": "", "isComposing": false}},
		{"MessageEvent", "Event", map[string]any{"data": nil, "origin": "", "lastEventId": "", "source": nil}},
		{"ErrorEvent", "Event", map[string]any{"message": "", "filename": "", "lineno": 0, "colno": 0, "error": nil}},
		{"ProgressEvent", "Event", map[string]any{"lengthComputable": false, "loaded": 0, "total": 0}},
	}
	defaults := map[string]map[string]any{}
	for _, d := range definitions {
		p := s.vm.NewObject()
		_ = p.SetPrototype(protos[d.parent])
		protos[d.name] = p
		fields := map[string]any{}
		for k, v := range defaults[d.parent] {
			fields[k] = v
		}
		for k, v := range d.fields {
			fields[k] = v
		}
		defaults[d.name] = fields
		s.specialEventDefaults[p] = fields
		for key := range d.fields {
			s.accessor(p, key, func(t goja.Value) goja.Value { return value(s.brand(t, "Event"), key) }, nil)
		}
		ctor := s.vm.ToValue(func(c goja.ConstructorCall) *goja.Object {
			if len(c.Arguments) == 0 {
				panic(s.vm.NewTypeError("missing event type"))
			}
			o := s.eventDictionary(c.Argument(1))
			e := s.newEvent(c.Argument(0).String(), flag(o, "bubbles"), flag(o, "cancelable"), flag(o, "composed"), c.This.Prototype())
			s.initializeSpecialEvent(e, p, o)
			return e
		}).(*goja.Object)
		_ = ctor.Set("prototype", p)
		_ = ctor.SetPrototype(s.vm.Get(d.parent).(*goja.Object))
		defineFrozen(p, "constructor", ctor)
		defineFrozen(s.vm.GlobalObject(), d.name, ctor)
		_ = p.DefineDataPropertySymbol(goja.SymToStringTag, s.vm.ToValue(d.name), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
		if d.name == "KeyboardEvent" || d.name == "MouseEvent" {
			s.method(p, "getModifierState", func(c goja.FunctionCall) goja.Value {
				st := s.brand(c.This, "Event")
				key := map[string]string{"Control": "ctrlKey", "Shift": "shiftKey", "Alt": "altKey", "Meta": "metaKey"}[c.Argument(0).String()]
				return s.vm.ToValue(key != "" && flag(st, key))
			})
		}
	}
}

func (s *Sandbox) initializeSpecialEvent(e, proto, options *goja.Object) {
	st := s.brand(e, "Event")
	for key, def := range s.specialEventDefaults[proto] {
		v := value(options, key)
		if goja.IsUndefined(v) {
			v = s.vm.ToValue(def)
		}
		switch def.(type) {
		case string:
			v = s.vm.ToValue(v.String())
		case bool:
			v = s.vm.ToValue(v.ToBoolean())
		case int:
			v = s.vm.ToValue(v.ToInteger())
		}
		_ = st.Set(key, v)
	}
}
