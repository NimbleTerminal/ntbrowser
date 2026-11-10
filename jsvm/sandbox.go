// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package jsvm owns the Goja runtime for one page. It is the hardened
// boundary: this package imports neither os, net, net/http, os/exec nor
// syscall (enforced by TestNoSystemImports), exposes no Go value to scripts by
// reflection (every host object is built property-by-property), and runs every
// entry into JavaScript under a time-slice watchdog.
//
// Why Goja: pure Go, no CGO, bytecode interpreter with no JIT, builds for
// GOOS=js like nthtml. Its default runtime contains only ECMAScript built-ins:
// there is no require, process, fs, XMLHttpRequest or fetch unless the host
// adds them. Hardening is therefore mostly about what we add and how.
package jsvm

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"

	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/sessionstorage"
)

// Config bounds one page's scripting. Zero fields use the defaults below.
type Config struct {
	FrameDOM                         func(DOMRequest) (DOMReply, error)
	CloseFrameDOM                    func()
	ScrollPosition                   func() (float64, float64)
	Scroll                           func(float64, float64, bool)
	FrameSelf, FrameParent, FrameTop uint64
	FrameOpaque                      bool
	EnsureFrame                      func(FrameSpec) (uint64, error)
	FrameAlive                       func(uint64) bool
	PostFrame                        func(FramePost) error

	MaxMessagePorts int                 // live ports (default 128)
	MaxMessages     int                 // queued messages per page (default 256)
	MaxMessageBytes int                 // total queued clone charge (default 4 MiB)
	MaxCodecBytes   int                 // text codec input/output cap per call (default 2 MiB)
	DynamicScripts  bool                // host-authorized classic script insertion
	LoadScript      func(ScriptRequest) // VM-thread admission; completion through CompleteScript

	SessionStorage   *sessionstorage.Area          // bound to one origin by the host; nil is closed
	LocalStorage     *sessionstorage.Area          // the host's local store for this origin; nil is closed
	Navigate         func(href, mode string) error // host-authorized navigation; nil is closed
	CookieRead       func() string                 // host-only capability, bound to the current document URL
	CookieWrite      func(string)                  // nil makes document.cookie writes inert
	AllowDynamicCode bool                          // native eval and Function-family compilation; default false
	Slice            time.Duration                 // wall-clock budget per entry into JS (default 50ms)
	Strikes          int                           // slice overruns before scripting is disabled (default 3)
	MaxCallStack     int                           // default 512 frames
	MaxTimers        int                           // live timers (default 256)
	MaxListeners     int                           // total event listeners (default 1024)
	HeapBudget       uint64                        // process heap bytes after GC above which this page is disabled (default 256 MiB)
	HeapGrace        time.Duration                 // how long the heap may stay above HeapBudget before the page is disabled (default 1s)
	Media            func() MediaState             // layout width, viewport height and color scheme for window.matchMedia (default 800×600 light)
	ClockResolution  time.Duration                 // minimum clock resolution (default 10ms), shared by Date, performance and rAF
	Clock            func() time.Time
	Rand             func() float64
	Location         string // read-only location.href shown to scripts
}

// Host is what the loop gives the sandbox. Every method is called on the VM
// goroutine. Completion callbacks the host receives must be invoked on the VM
// goroutine as well, never from a worker.
type Host interface {
	// QueueTask schedules one future task under the normal watchdog/timer cap.
	// ClearTimer must not cancel these private task handles.
	QueueTask(goja.Callable) (int, bool)
	ClearTask(int)
	SetTimer(delay time.Duration, repeat bool, fn goja.Callable, args []goja.Value) (id int, ok bool)
	ClearTimer(id int)
	RequestFrame(fn goja.Callable) int
	CancelFrame(id int)
	// Fetch starts a request and later calls done exactly once on the VM goroutine.
	Fetch(req FetchRequest, done func(FetchResponse, error)) error
	Log(level string, line string)
	// Later runs fn on the VM goroutine after delay, outside any entry, unless
	// the page has been replaced or dropped. It is private to the host: pages
	// cannot see or cancel it, and it is not counted against their timers.
	Later(delay time.Duration, fn func())
}

type FetchRequest struct {
	OmitCookies       bool
	Method, URL, Body string
	Headers           map[string]string
}

type FetchResponse struct {
	Status     int
	StatusText string
	URL        string
	Headers    map[string]string
	Body       string
}

// Sandbox state is grouped by feature. Each group is an embedded struct, so
// its fields read as the sandbox's own, and Close releases a group with one
// assignment: a field added to a group is released without touching Close
// (TestCloseReleasesEveryReference checks the rest).

// mutationFields is MutationObserver state (mutations.go).
type mutationFields struct {
	mutationProto, mutationPromise                                                                *goja.Object
	mutationNotify                                                                                goja.Value
	mutationThen                                                                                  goja.Callable
	mutationNext                                                                                  uint64
	mutationObservers                                                                             map[uint64]*mutationObserver
	mutationOrder, mutationPending                                                                []uint64
	mutationPins                                                                                  map[dom.NodeID]int
	mutationRegistrations, mutationRecords, mutationBytes, mutationTransients, mutationDeliveries int
	mutationScheduled, mutationStopped                                                            bool
}

// frameFields is child frames and shared frame DOM handles.
type frameFields struct {
	foreignNodes    map[DOMRef]*goja.Object
	foreignRefs     map[*goja.Object]DOMRef
	foreignPins     map[dom.NodeID]int
	iframeProto     *goja.Object
	frameWindows    map[uint64]*goja.Object
	frameChildCount int
}

// messageFields is message channels and window.postMessage.
type messageFields struct {
	messageOrigin             string
	messagePorts              map[dom.NodeID]*messagePort
	portMessages              []portMessage
	messageBytes, messageTask int
	messageTaskPending        bool
	cloneMessage              func(goja.Value) (goja.Value, int)
	emptyMessagePorts         *goja.Object
}

// eventFields is event targets, dispatch, abort signals and rejection
// reporting. The listener counts are in bindingFields with the wrappers
// they mirror.
type eventFields struct {
	signalPins             map[dom.NodeID]*goja.Object
	specialEventProtos     map[string]*goja.Object
	specialEventDefaults   map[*goja.Object]map[string]any
	domExceptionProto      *goja.Object
	eventTargetProto       *goja.Object
	eventProto             *goja.Object
	customEventProto       *goja.Object
	signalProto            *goja.Object
	eventPrivate           *goja.Object
	privateGet, privateSet goja.Callable
	extraTargetNext        dom.NodeID
	extraTargets           map[dom.NodeID]bool
	abortTimers            map[dom.NodeID]int
	eventDepth             int
	pendingRejections      []pendingRejection
	rejectionsDropped      bool
	rejectionReporting     bool
	eventErrorReporting    bool
}

// resourceFields is scripts, document.write and images.
type resourceFields struct {
	writeScriptWarned bool
	writeScript       dom.NodeID // script whose document.write insertion point writeCursor follows
	writeCursor       dom.NodeID
	scriptProto       *goja.Object
	scripts           map[dom.NodeID]*scriptState
	scriptCount       int
	currentScript     dom.NodeID
	imageProto        *goja.Object
	images            map[dom.NodeID]*imageState
	imageDecodes      int
	imageSequence     uint64
}

// bindingFields is the DOM bindings: prototypes, symbols, node wrappers and
// the listener counts that mirror them.
type bindingFields struct {
	styleProto        *goja.Object
	styleSym          *goja.Symbol
	collectionProto   *goja.Object
	mediaLists        []*mediaList
	urlSym, paramsSym *goja.Symbol
	anchorProto       *goja.Object
	relListSym        *goja.Symbol
	elemProto         *goja.Object
	nodeSym           *goja.Symbol // wrapper -> NodeID
	// weakW holds every wrapper weakly; strong pins listener-bearing wrappers
	// of attached nodes. See bindings.go for the lifetime rules.
	weakW  map[dom.NodeID]weak.Pointer[goja.Object]
	strong map[dom.NodeID]*goja.Object
	// listenCount[node][type] mirrors the callbacks stored on wrappers, as
	// plain integers, so budgets survive wrapper collection.
	listenCount map[dom.NodeID]map[string]int
	typeCount   map[string]int
	nListen     int
	active      dom.NodeID // focused element, 0 = none
	forms       FormHooks  // host hooks for submit/reset/value writes (forms.go)
	docObj      *goja.Object
	jsonParse   goja.Callable
	promiseCtor *goja.Object
}

// newBindingFields is the empty state: the maps exist because wrapper
// finalizers and late host calls may still write after Close.
func newBindingFields() bindingFields {
	return bindingFields{
		weakW:       map[dom.NodeID]weak.Pointer[goja.Object]{},
		strong:      map[dom.NodeID]*goja.Object{},
		listenCount: map[dom.NodeID]map[string]int{},
		typeCount:   map[string]int{},
	}
}

type Sandbox struct {
	mutationFields
	frameFields
	messageFields
	eventFields
	resourceFields
	bindingFields

	// The runtime itself and what outlives a page's state.
	documentReady     string
	performanceOrigin float64
	performanceNow    func() float64
	vm                *goja.Runtime
	cfg               Config
	host              Host
	tree              *dom.Tree
	strikes           int
	heapOver          time.Time // first over-budget sample after a collection; zero while under budget
	disabled          bool
	running           bool
	collectedMu       sync.Mutex
	collected         []dom.NodeID // NodeIDs whose wrappers the GC reclaimed
	wd                watchdog
}

// watchdog is the time dimension of the sandbox: one timer per entry into
// JS that calls Runtime.Interrupt when the slice expires. It can be paused
// while the bridge does Go-side work on the page's behalf (forced GC under
// budget pressure) so that work is not billed to the script.
type watchdog struct {
	vm       *goja.Runtime
	timer    *time.Timer
	fired    chan struct{}
	deadline time.Time
}

func (w *watchdog) arm(d time.Duration) {
	w.fired = make(chan struct{})
	w.deadline = time.Now().Add(d)
	fired := w.fired
	w.timer = time.AfterFunc(d, func() { w.vm.Interrupt("time slice exceeded"); close(fired) })
}

// disarm stops the timer. If the timer won the race, it waits for Interrupt
// to land and clears it so a late interrupt cannot hit the next, innocent
// entry. It reports whether the slice had expired.
func (w *watchdog) disarm() (expired bool) {
	if w.timer == nil {
		return false
	}
	if !w.timer.Stop() {
		<-w.fired
		w.vm.ClearInterrupt()
		expired = true
	}
	w.timer = nil
	return expired
}

// pause stops the clock and returns a resume function that re-arms it with
// the time that was left (zero if it had already expired, which interrupts
// the script at its next instruction, as it should).
func (w *watchdog) pause() (resume func()) {
	remaining := time.Until(w.deadline)
	if w.disarm() || remaining < 0 {
		remaining = 0
	}
	return func() { w.arm(remaining) }
}

var ErrDisabled = errors.New("ntbrowser/jsvm: scripting disabled for this page")

// maxScriptBytes bounds one script source (parse work is roughly linear).
const maxScriptBytes = 1 << 20

func New(cfg Config, tree *dom.Tree, host Host) *Sandbox {
	if cfg.Slice <= 0 {
		cfg.Slice = 50 * time.Millisecond
	}
	if cfg.Strikes <= 0 {
		cfg.Strikes = 3
	}
	if cfg.MaxCallStack <= 0 {
		cfg.MaxCallStack = 512
	}
	if cfg.MaxCodecBytes <= 0 {
		cfg.MaxCodecBytes = defaultCodecLimit
	}
	if cfg.MaxMessagePorts <= 0 {
		cfg.MaxMessagePorts = 128
	}
	if cfg.MaxMessages <= 0 {
		cfg.MaxMessages = 256
	}
	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = 4 << 20
	}
	if cfg.MaxTimers <= 0 {
		cfg.MaxTimers = 256
	}
	if cfg.MaxListeners <= 0 {
		cfg.MaxListeners = 1024
	}
	if cfg.HeapBudget == 0 {
		cfg.HeapBudget = 256 << 20
	}
	if cfg.HeapGrace <= 0 {
		cfg.HeapGrace = time.Second
	}
	origin, now := prepareClocks(&cfg)
	if cfg.Rand == nil {
		cfg.Rand = RandomSource(0) // fresh OS-keyed generator for this page
	}
	s := &Sandbox{cfg: cfg, host: host, tree: tree, performanceOrigin: origin, performanceNow: now,
		bindingFields: newBindingFields()}
	s.vm = goja.New()
	s.wd.vm = s.vm
	s.harden()
	s.nodeSym = goja.NewSymbol("ntbrowser.node")
	s.installEventCore()
	s.elemProto = s.buildElementProto()
	s.installStyle()
	s.installAnchors()
	s.installImages()
	s.installScripts()
	s.installFrames()
	s.installCollections()
	s.installGlobals()
	s.installScroll()
	s.installStorage()
	s.installPerformance()
	s.installEncoding()
	s.installCrypto()
	s.installMessages()
	s.installWindowMessages()
	s.installMutations()
	dom.Walk(tree.Root(), func(n *dom.Node) {
		if n.Tag() == "img" {
			s.imageChanged(n)
		}
	})
	return s
}

// harden configures the interpreter itself, before any host API exists.
func (s *Sandbox) harden() {
	vm := s.vm
	// Goja's parser honours "//# sourceMappingURL=" by default and its default
	// loader reads the referenced path with os.ReadFile. Disabling source maps
	// removes the only filesystem touch point inside the engine.
	vm.SetParserOptions(parser.WithDisableSourceMaps)
	// Infinite recursion becomes a catchable StackOverflowError instead of a
	// multi-gigabyte Go stack.
	vm.SetMaxCallStackSize(s.cfg.MaxCallStack)
	// Deterministic, coarse, host-controlled time and randomness.
	vm.SetTimeSource(goja.Now(s.cfg.Clock))
	vm.SetRandSource(goja.RandSource(s.cfg.Rand))
	// Unhandled rejections surface in the terminal log instead of vanishing.
	vm.SetPromiseRejectionTracker(s.trackPromiseRejection)
	// Capture pristine built-ins before any script can monkey-patch them.
	s.jsonParse, _ = goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	s.promiseCtor = vm.Get("Promise").ToObject(vm)
	s.configureDynamicCode()
}

// defineFrozen installs a host API as non-writable, non-configurable. Scripts
// cannot shadow fetch or document with their own implementations, and a
// Proxy-wrapped replacement cannot be swapped in later.
func defineFrozen(obj *goja.Object, name string, v goja.Value) {
	_ = obj.DefineDataProperty(name, v, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

func (s *Sandbox) installGlobals() {
	vm := s.vm
	global := vm.GlobalObject()
	s.registerEventTarget(global, 0)
	_ = global.SetPrototype(s.eventTargetProto)
	s.bindEventTarget(global)
	s.bindEventHandlers(global)
	defineFrozen(global, "window", global)
	defineFrozen(global, "self", global)
	// Realm-local aliases and isolated parent/top proxies. Replaceable aliases
	// are configurable so ordinary
	// global lexical declarations (for example, let frames = 0) remain valid.
	defineFrozen(global, "top", s.frameWindow(s.cfg.FrameTop))
	for _, name := range []string{"parent", "frames"} {
		v := global
		if name == "parent" {
			v = s.frameWindow(s.cfg.FrameParent)
		}
		_ = global.DefineDataProperty(name, v, goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	}
	_ = global.DefineDataProperty("length", vm.ToValue(0), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	_ = global.DefineDataProperty("opener", goja.Null(), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	_ = global.DefineAccessorProperty("frameElement", vm.ToValue(func(goja.FunctionCall) goja.Value { return goja.Null() }), nil, goja.FLAG_TRUE, goja.FLAG_TRUE)
	_ = global.DefineAccessorProperty("closed", vm.ToValue(func(goja.FunctionCall) goja.Value { return vm.ToValue(false) }), nil, goja.FLAG_TRUE, goja.FLAG_TRUE)
	defineFrozen(global, "console", s.consoleObject())
	doc := s.documentObject()
	defineFrozen(doc, "defaultView", global)
	defineFrozen(global, "document", doc)
	defineFrozen(global, "fetch", vm.ToValue(s.fetch))
	s.installLocation()
	s.installHistory()
	s.installMatchMedia()
	s.installURL()
	navigator := vm.NewObject()
	defineFrozen(navigator, "cookieEnabled", vm.ToValue(s.cfg.CookieRead != nil && s.cfg.CookieWrite != nil))
	defineFrozen(navigator, "userAgent", vm.ToValue("ntbrowser"))
	defineFrozen(global, "navigator", navigator)
	defineFrozen(global, "setTimeout", vm.ToValue(s.timerFunc(false)))
	defineFrozen(global, "setInterval", vm.ToValue(s.timerFunc(true)))
	defineFrozen(global, "clearTimeout", vm.ToValue(s.clearTimer))
	defineFrozen(global, "clearInterval", vm.ToValue(s.clearTimer))
	defineFrozen(global, "requestAnimationFrame", vm.ToValue(s.requestFrame))
	defineFrozen(global, "cancelAnimationFrame", vm.ToValue(s.cancelFrame))
}

// Run is the single entry point into JavaScript. It arms the watchdog, runs
// fn, and converts every failure mode into an error the loop can log.
// Goja drains the promise job queue when the outermost call returns, so
// microtasks are included in the slice.
func (s *Sandbox) Run(fn func(vm *goja.Runtime) error) (err error) {
	if s.disabled {
		return ErrDisabled
	}
	if s.running {
		return errors.New("ntbrowser/jsvm: re-entrant Run")
	}
	s.running = true
	s.reap() // apply GC reports before the page runs again
	s.vm.ClearInterrupt()
	s.wd.arm(s.cfg.Slice)
	s.mutationDeliveries = 0
	defer func() {
		s.pendingRejections = nil
		s.rejectionsDropped = false
		s.wd.disarm()
		s.running = false
		s.checkHeap()
		// An interrupted Goja entry drops its queued jobs. Re-arm notifications
		// at the next owner checkpoint instead of leaving them stranded.
		s.mutationScheduled = false
		if r := recover(); r != nil {
			// A Go panic inside a binding must never take the loop down.
			err = fmt.Errorf("ntbrowser/jsvm: binding panic: %v", r)
			s.strike()
		}
		if err != nil {
			s.mutationPending = nil
			for _, id := range s.mutationOrder {
				o := s.mutationObservers[id]
				o.pending = false
				if len(o.records) > 0 || len(o.transients) > 0 {
					s.pendingMutation(id, o)
				}
			}
		}
	}()
	s.scheduleMutations()
	err = fn(s.vm)
	if err == nil {
		s.scheduleMutations()
	}
	var interrupted *goja.InterruptedError
	var overflow *goja.StackOverflowError
	var exception *goja.Exception
	// Goja has drained the microtask queue when fn returns. Report only promises
	// still unhandled, while retaining this turn's active watchdog.
	if !errors.As(err, &interrupted) && !errors.As(err, &overflow) {
		if reportErr := s.flushPromiseRejections(); reportErr != nil {
			err = reportErr
		}
	}
	switch {
	case errors.As(err, &interrupted):
		s.strike()
		return fmt.Errorf("ntbrowser/jsvm: %v (strike %d/%d)", interrupted.Value(), s.strikes, s.cfg.Strikes)
	case errors.As(err, &overflow):
		s.strike()
		return err
	case errors.As(err, &exception):
		message := safeString(exception.Value())
		// Use the captured frames, not a page-controlled Error.stack property.
		// Callbacks retain their original source location here as well.
		location := ""
		for _, frame := range exception.Stack() {
			pos := frame.Position()
			if pos.Line > 0 {
				location = fmt.Sprintf(" at %s:%d:%d", pos.Filename, pos.Line, pos.Column)
				break
			}
		}
		if name := excludedGlobal(exception.Value()); name != "" {
			return fmt.Errorf("ntbrowser/jsvm: script uses %s, %w%s", name, ErrUnsupportedAPI, safeString(s.vm.ToValue(location)))
		}
		return fmt.Errorf("ntbrowser/jsvm: uncaught %s", safeString(s.vm.ToValue(message+location)))
	}
	return err
}

// ErrUnsupportedAPI marks a script that stopped on a global the design leaves
// out on purpose (spec §6: no XMLHttpRequest, WebSocket, Worker, WebAssembly,
// shared memory). The host reports it as a gap in this browser rather than as
// an error in the page.
var ErrUnsupportedAPI = errors.New("which this browser does not provide")

var excludedGlobals = map[string]bool{"XMLHttpRequest": true, "WebSocket": true, "Worker": true, "SharedWorker": true, "WebAssembly": true, "SharedArrayBuffer": true, "Atomics": true, "importScripts": true, "require": true, "process": true}

// excludedGlobal names the design-excluded global behind a native
// ReferenceError, or "" for any other exception. The check reads the engine's
// own error class and message shape; a page can only influence the log level.
func excludedGlobal(v goja.Value) string {
	obj, ok := v.(*goja.Object)
	if !ok || obj.ClassName() != "Error" || obj.Get("name").String() != "ReferenceError" {
		return ""
	}
	name, ok := strings.CutSuffix(obj.Get("message").String(), " is not defined")
	if !ok || !excludedGlobals[name] {
		return ""
	}
	return name
}

func (s *Sandbox) strike() {
	s.strikes++
	if s.strikes >= s.cfg.Strikes {
		s.disabled = true
		s.host.Log("error", "scripting disabled: repeated time slice overruns")
	}
}

func (s *Sandbox) Disabled() bool { return s.disabled }

// Close disables the page's scripting for good and drops every reference to
// the Goja runtime and the wrappers so the GC can reclaim the JS heap. The
// loop calls it when the heap budget or the strike budget is exhausted and
// when a page is replaced; after Close every entry point returns ErrDisabled
// and Dispatch reports "not prevented". The dom.Tree is left to its owner.
func (s *Sandbox) Close() {
	s.disabled = true
	// Cleanup that reaches outside the sandbox comes first, while the state
	// it reads is still there.
	s.tree.SetMutationHook(nil)
	if s.messageTaskPending {
		s.host.ClearTask(s.messageTask)
	}
	if s.cfg.CloseFrameDOM != nil {
		s.cfg.CloseFrameDOM()
	}
	for _, id := range s.abortTimers {
		s.host.ClearTimer(id)
	}
	s.wd.disarm()
	// Then every feature's state goes at once.
	s.mutationFields = mutationFields{}
	s.frameFields = frameFields{}
	s.messageFields = messageFields{}
	s.eventFields = eventFields{}
	s.resourceFields = resourceFields{}
	s.bindingFields = newBindingFields()
	s.cfg.LoadScript = nil
	s.cfg.SessionStorage = nil
	s.cfg.LocalStorage = nil
	s.performanceNow = nil
	s.vm = nil
	s.wd.vm = nil
	s.collectedMu.Lock()
	s.collected = nil
	s.collectedMu.Unlock()
}

// Eval compiles and runs page script source in sloppy mode with a stable
// name for stack traces. Compile errors are returned, not thrown.
//
// It must NOT use goja.Compile: that package-level function parses with no
// parser options (runtime.go: compile(name, src, strict, true, nil)), so the
// WithDisableSourceMaps set on the runtime would be ignored and a
// "//# sourceMappingURL=/etc/passwd" comment would reach os.ReadFile.
// Parse with explicit options, then CompileAST.
// ErrModuleSyntax marks a classic script that fails to parse only because it
// uses ES module syntax. Goja has no module loader, so a dynamic import() is
// an unsupported feature of this browser, not a syntax error in the page.
var ErrModuleSyntax = errors.New("ES modules are not supported")

// ErrUnsupportedSyntax marks a script the engine's parser rejects for using a
// language feature it has not implemented (Goja reports these as "… not
// supported yet"), as opposed to a syntax error in the page's own code.
var ErrUnsupportedSyntax = errors.New("syntax this engine does not support")

// EvalScript runs a parser-inserted script with document.currentScript set
// to its element for the duration, as browsers do for classic scripts.
func (s *Sandbox) EvalScript(name, src string, script dom.NodeID) error {
	previous := s.currentScript
	s.currentScript = script
	defer func() { s.currentScript = previous }()
	return s.Eval(name, src)
}

func (s *Sandbox) Eval(name, src string) error {
	if len(src) > maxScriptBytes {
		return fmt.Errorf("ntbrowser/jsvm: script %s over %d bytes", name, maxScriptBytes)
	}
	ast, err := goja.Parse(name, src, sandboxParserOptions()...)
	if err != nil {
		if usesDynamicImport(src, err) {
			return fmt.Errorf("%w: the script uses import()", ErrModuleSyntax)
		}
		return classifyCompileError(err)
	}
	prg, err := goja.CompileAST(ast, false)
	if err != nil {
		return classifyCompileError(err)
	}
	return s.Run(func(vm *goja.Runtime) error { _, err := vm.RunProgram(prg); return err })
}

// Call invokes a stored callback under the watchdog.
func (s *Sandbox) Call(fn goja.Callable, this goja.Value, args ...goja.Value) error {
	return s.Run(func(vm *goja.Runtime) error { _, err := fn(this, args...); return err })
}

// CallWithinBudget preserves the per-entry watchdog while shortening it to
// the remaining host batch budget. Only the VM owner may call this method.
func (s *Sandbox) CallWithinBudget(budget time.Duration, fn goja.Callable, this goja.Value, args ...goja.Value) error {
	if budget <= 0 {
		return errors.New("animation frame budget exhausted")
	}
	previous := s.cfg.Slice
	s.cfg.Slice = min(previous, budget)
	defer func() { s.cfg.Slice = previous }()
	return s.Call(fn, this, args...)
}

// ---- timers and frames: thin shims over Host ----

func (s *Sandbox) timerFunc(repeat bool) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(s.vm.NewTypeError("callback must be a function; string callbacks are not supported"))
		}
		delay := time.Duration(call.Argument(1).ToFloat() * float64(time.Millisecond))
		if delay < 4*time.Millisecond && repeat {
			delay = 4 * time.Millisecond // browser clamp; prevents timer storms
		}
		if delay < 0 {
			delay = 0
		}
		var args []goja.Value
		if len(call.Arguments) > 2 {
			args = append(args, call.Arguments[2:]...)
		}
		id, ok := s.host.SetTimer(delay, repeat, fn, args)
		if !ok {
			panic(jsError(s.vm, errors.New("too many timers")))
		}
		return s.vm.ToValue(id)
	}
}

func (s *Sandbox) clearTimer(call goja.FunctionCall) goja.Value {
	s.host.ClearTimer(int(call.Argument(0).ToInteger()))
	return goja.Undefined()
}

func (s *Sandbox) requestFrame(call goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(s.vm.NewTypeError("callback must be a function"))
	}
	return s.vm.ToValue(s.host.RequestFrame(fn))
}

func (s *Sandbox) cancelFrame(call goja.FunctionCall) goja.Value {
	s.host.CancelFrame(int(call.Argument(0).ToInteger()))
	return goja.Undefined()
}

// ---- console: terminal-safe logging ----

func (s *Sandbox) consoleObject() *goja.Object {
	c := s.vm.NewObject()
	for _, level := range []string{"log", "info", "warn", "error", "debug"} {
		level := level
		defineFrozen(c, level, s.vm.ToValue(func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				parts = append(parts, safeString(a))
			}
			s.host.Log(level, strings.Join(parts, " "))
			return goja.Undefined()
		}))
	}
	return c
}

// safeString renders a JS value for a terminal: bounded length and no C0/C1
// control characters, so a page cannot inject escape sequences (OSC, DCS,
// cursor moves, Kitty graphics commands) into the host's status line or log.
// jsError is the only way the sandbox turns a Go error into something the
// page can catch: a plain TypeError whose message is control-stripped and
// capped. Never goja.NewGoError, which attaches the Go error itself as a
// reflected `value` property with readable, callable and writable Go fields
// (spec §2 anti-goal; TestOnlyHardenedCompilePath forbids it statically,
// TestErrorsNeverCarryGoValues checks what a page can see).
func jsError(vm *goja.Runtime, err error) *goja.Object {
	return vm.NewTypeError(safeString(vm.ToValue(err.Error())))
}

func safeString(v goja.Value) string {
	if v == nil {
		return "undefined"
	}
	var str string
	if obj, ok := v.(*goja.Object); ok && obj.ClassName() != "Error" {
		if b, err := obj.MarshalJSON(); err == nil {
			str = string(b)
		} else {
			str = v.String()
		}
	} else {
		str = v.String()
	}
	const maxLog = 4096
	if len(str) > maxLog {
		str = str[:maxLog] + "…"
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
			if r == '\n' || r == '\t' {
				return ' '
			}
			return -1
		}
		return r
	}, str)
}

// ToValue converts plain Go scalars/strings for the loop (e.g. rAF
// timestamps). It must never be handed a struct or pointer: goja would
// reflect over it and expose Go fields to scripts.
func (s *Sandbox) ToValue(v any) goja.Value {
	switch v.(type) {
	case nil, bool, string, int, int64, float64:
		return s.vm.ToValue(v)
	}
	panic("jsvm: refusing to reflect a Go value into the sandbox")
}

// checkHeap is the memory dimension of the watchdog. Goja cannot cap its own
// heap, and a script can allocate hundreds of MB inside one 50 ms slice
// without ever tripping Interrupt. After every entry heap objects are sampled.
// This process-wide metric includes renderer allocations and garbage awaiting
// collection, not just the page's JavaScript. If over budget, collect once and
// resample before disabling the page. Run has already disarmed the watchdog,
// so collection is not billed as script execution. The loop then drops the
// runtime so the GC can reclaim it. This
// degrades the page instead of letting debug.SetMemoryLimit thrash or the
// process die, but a single pathological slice can still overshoot.
func processHeapBytes() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() == metrics.KindUint64 {
		return sample[0].Value.Uint64()
	}
	return 0
}

// checkHeap disables the page when the process heap stays above HeapBudget
// for HeapGrace. The metric is process-wide: the host's own frame encoding
// (a terminal raster, its retained copy and compressed frames, alive for a
// few hundred milliseconds while the next frame starts) can push one sample
// over the budget, so one high sample only starts the grace and schedules a
// recheck through the host. A retained allocation bomb is still over budget at
// that recheck and is disabled then; a sample back under budget ends the grace.
func (s *Sandbox) checkHeap() {
	if s.disabled || s.vm == nil {
		return
	}
	if processHeapBytes() <= s.cfg.HeapBudget {
		s.heapOver = time.Time{}
		return
	}
	runtime.GC()
	used := processHeapBytes()
	if used <= s.cfg.HeapBudget {
		s.heapOver = time.Time{}
		return
	}
	now := time.Now()
	if s.heapOver.IsZero() {
		s.heapOver = now
		s.host.Log("warn", fmt.Sprintf("heap budget exceeded (process heap %d bytes after GC; budget %d bytes); scripting is disabled if it stays above the budget for %v", used, s.cfg.HeapBudget, s.cfg.HeapGrace))
		s.host.Later(s.cfg.HeapGrace, s.recheckHeap)
		return
	}
	if now.Sub(s.heapOver) < s.cfg.HeapGrace {
		return
	}
	s.disabled = true
	s.host.Log("error", fmt.Sprintf("scripting disabled: heap budget exceeded for %v (process heap %d bytes after GC; budget %d bytes)", s.cfg.HeapGrace, used, s.cfg.HeapBudget))
}

// recheckHeap is the host-scheduled end of a heap grace. Inside an entry the
// entry's own check follows; a closed or disabled runtime has nothing to check.
func (s *Sandbox) recheckHeap() {
	if s.running {
		return
	}
	s.checkHeap()
}

// sandboxParserOptions is the single definition of how page source is parsed.
func sandboxParserOptions() []parser.Option { return []parser.Option{parser.WithDisableSourceMaps} }

// classifyCompileError turns Goja's "… not supported yet" rejections, which
// the compiler raises for features such as async generators, into
// ErrUnsupportedSyntax; other parse and compile errors pass through.
func classifyCompileError(err error) error {
	if feature, ok := unsupportedFeature(err); ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedSyntax, feature)
	}
	return err
}

// unsupportedFeature reports a parse or compile failure that names an
// unimplemented language feature, with the feature's description.
func unsupportedFeature(err error) (string, bool) {
	var syntax *goja.CompilerSyntaxError
	if !errors.As(err, &syntax) {
		return "", false
	}
	feature, ok := strings.CutSuffix(strings.TrimSpace(syntax.Message), "are not supported yet")
	if !ok {
		feature, ok = strings.CutSuffix(strings.TrimSpace(syntax.Message), "is not supported yet")
	}
	if !ok {
		return "", false
	}
	return strings.TrimSpace(feature), true
}

// usesDynamicImport reports whether a parse failure sits on an import( call,
// the one piece of module syntax a classic script may legitimately contain.
// The parser reports the reserved word's own byte offset, so the check reads a
// few bytes there and never rescans the source.
func usesDynamicImport(src string, err error) bool {
	var syntax *goja.CompilerSyntaxError
	if !errors.As(err, &syntax) || syntax.Message != "Unexpected reserved word" || syntax.Offset < 0 || syntax.Offset >= len(src) {
		return false
	}
	rest, ok := strings.CutPrefix(src[syntax.Offset:], "import")
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(rest, " \t\r\n"), "(")
}

// Reap applies pending GC reports now (the loop calls it after runtime.GC
// when the node budget is hit; Run calls it on every entry).
func (s *Sandbox) Reap() { s.reap() }
