package jsvm

import (
	"errors"

	"github.com/dop251/goja"
)

// fetch(url, init?) returns a Promise. The sandbox never touches a socket:
// it builds a FetchRequest out of plain strings, hands it to Host.Fetch, and
// settles the promise when the host calls back on the VM goroutine. Only the
// subset of the Fetch API that reader-mode pages need is modelled: method,
// headers, body, and a Response with ok/status/headers/text()/json().
func (s *Sandbox) fetch(call goja.FunctionCall) goja.Value {
	vm := s.vm
	promise, resolve, reject := vm.NewPromise()
	req := FetchRequest{URL: call.Argument(0).String(), Method: "GET", Headers: map[string]string{}}
	if init, ok := call.Argument(1).(*goja.Object); ok {
		if m := init.Get("method"); m != nil && !goja.IsUndefined(m) {
			req.Method = m.String()
		}
		if b := init.Get("body"); b != nil && !goja.IsUndefined(b) && !goja.IsNull(b) {
			req.Body = b.String() // only string bodies; no Blob/FormData/streams
		}
		if h, ok := init.Get("headers").(*goja.Object); ok {
			for _, k := range h.Keys() {
				if len(req.Headers) >= 8 {
					break
				}
				req.Headers[k] = h.Get(k).String()
			}
		}
	}
	err := s.host.Fetch(req, func(resp FetchResponse, err error) {
		// Runs on the VM goroutine, outside any Run slice, so settle under one.
		_ = s.Run(func(vm *goja.Runtime) error {
			if err != nil {
				return reject(vm.NewGoError(err))
			}
			return resolve(s.responseObject(resp))
		})
	})
	if err != nil {
		_ = reject(vm.NewGoError(err))
	}
	return vm.ToValue(promise)
}

func (s *Sandbox) responseObject(r FetchResponse) *goja.Object {
	vm := s.vm
	o := vm.NewObject()
	defineFrozen(o, "ok", vm.ToValue(r.Status >= 200 && r.Status < 300))
	defineFrozen(o, "status", vm.ToValue(r.Status))
	defineFrozen(o, "statusText", vm.ToValue(r.StatusText))
	defineFrozen(o, "url", vm.ToValue(r.URL))
	headers := vm.NewObject()
	defineFrozen(headers, "get", vm.ToValue(func(c goja.FunctionCall) goja.Value {
		if v, ok := r.Headers[toLowerASCII(c.Argument(0).String())]; ok {
			return vm.ToValue(v)
		}
		return goja.Null()
	}))
	defineFrozen(o, "headers", headers)
	consumed := false
	body := func(parse bool) func(goja.FunctionCall) goja.Value {
		return func(goja.FunctionCall) goja.Value {
			p, resolve, reject := vm.NewPromise()
			if consumed {
				_ = reject(vm.NewTypeError("body already consumed"))
				return vm.ToValue(p)
			}
			consumed = true
			if !parse {
				_ = resolve(vm.ToValue(r.Body))
				return vm.ToValue(p)
			}
			// Use the JSON.parse captured at startup, not whatever the page
			// has since assigned to globalThis.JSON.parse.
			v, err := s.jsonParse(goja.Undefined(), vm.ToValue(r.Body))
			if err != nil {
				_ = reject(errValue(vm, err))
			} else {
				_ = resolve(v)
			}
			return vm.ToValue(p)
		}
	}
	defineFrozen(o, "text", vm.ToValue(body(false)))
	defineFrozen(o, "json", vm.ToValue(body(true)))
	return o
}

func errValue(vm *goja.Runtime, err error) goja.Value {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		return ex.Value()
	}
	return vm.NewGoError(err)
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
