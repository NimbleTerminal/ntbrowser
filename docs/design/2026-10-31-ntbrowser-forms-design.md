# ntbrowser: forms (focus, editing, GET submission) — design

Date: 2026-10-31 · Status: approved 2026-10-31 (Evan); implemented (focus ring through nthtml v0.2.1, §9) · Author: Claude
Extends: `2026-10-25-ntbrowser-architecture-design.md` and `2026-10-25-ntbrowser-subresources-design.md` (both implemented). Everything there stands; this document adds form interaction. Evan asked for the design without a question round, so every decision below is mine unless marked; §10 lists the ones most worth checking.

## 1. Intent

Since nthtml v0.2.0, ntbrowser draws form controls: google.com shows its search box and both buttons, and every control is a `Box` with a source ID. None of them can be used. The reader cannot type a query, tick a box or press a button, so any page whose point is a form (search, filters, settings, comment boxes) is read-only.

This round makes forms work the way a reader expects from a browser: Tab to a field, type, press Enter, land on the results page. It covers GET forms end to end and lays down the state model POST will use later.

## 2. Goals, non-goals, anti-goals

**Goals**
- One keyboard focus for the page, shared by links and controls, moved with Tab / Shift+Tab and by clicking, visible as a ring, and identical to the page's `document.activeElement`.
- Text entry for `input` (text-like types, `password`, `number`) and `textarea`, with the current value drawn in the page as it is typed.
- Checkboxes, radio groups, `select` (single and `multiple`), `button` / `input type=submit|reset|button`, and `label` activation.
- Submission of GET forms: Enter in a field (implicit submission) or a submit button builds the form data set, encodes it into the action URL and navigates through the existing navigation rules (`NavMode`, netpolicy).
- The JS surface pages actually use: `value`, `checked`, `selectedIndex`, `options`, `form`, `elements`, `submit()`, `requestSubmit()`, `reset()`, and `focus`, `blur`, `input`, `change`, `submit` and `reset` events.

**Non-goals (this round)**
- POST and other methods. A POST form shows a status-line message and does not submit (§5 decision 12). The state model and `netpolicy.Request.Body` already allow it; it gets its own round, with history and resubmission rules.
- `input type=file`, `image`, `color`, date/time pickers, `datalist`, `contenteditable`.
- Constraint validation (`required`, `pattern`, `min`/`max`) and its UI. Forms submit as if `novalidate` were set.
- Autofill, saved passwords, spellcheck.
- Mouse text selection inside a field, IME composition.

**Anti-goals (a failure even if it works)**
- A page that can capture the reader's keystrokes without the reader choosing a field. Page script can move DOM focus, but only the reader starts text entry (decision 4).
- Typed data leaving the machine any way other than a submission the reader triggered, through the same navigation gate as a link.
- A form round that slows ordinary browsing: pages without forms must not pay a frame or a VM call they do not pay today.
- Page-controlled text (values set by script, option labels, placeholders) reaching the terminal without `app.clean`.

## 3. What recon established

- **nthtml (v0.2.0)** draws controls from attributes and children only, with no state of its own. The agreed contract is that the host reflects the current value into attributes and children before each render. Every control produces a `Box` whose `Anchor` and `SourceID` are its `id`, or `nthtml-control-N` when it has none. N is nthtml's parse sequence number, which ntbrowser cannot map back to a DOM node.
- **The serializer** (`dom.Tree.Serialize`) already mints `id="nt-h-N"` (N = `dom.NodeID`) for elements with listeners and no id, and `Tree.ByID` resolves those. `loop.ClickAt` uses `Box.Anchor` with `Tree.ByID` to find the clicked element.
- **Focus** exists in `jsvm`: `Sandbox.active`, `Sandbox.Focus(id)` (from host clicks), `element.focus()`/`blur()`, `document.activeElement`, and `focusable()` (`a`, `button`, `input`, `select`, `textarea`, `summary`, `[tabindex]`). No focus or blur events are dispatched, and nothing draws focus.
- **Keys** reach the page as a `keydown` copy (`loop.Input`, only when a listener exists). The viewer owns Tab / Shift+Tab / Enter for its own link focus (`Model.NextLink`, `ActivateLink`, emitting `LinkMsg`), which covers links only.
- **The viewer** exposes `NavigateSource(id)` (scroll an element into view), `CellToDocument` (now public; `app/cell.go` can be deleted), `SourceAtCell` and link focus. It has no way to focus or highlight a non-link element. nthtml supports neither CSS `outline` nor `:focus`.
- **jsvm** has no `value`, `checked`, `selectedIndex`, `form` or `submit` bindings, and `Dispatch` always bubbles and has no non-bubbling events.
- **netpolicy** `Request` already has `Method` and `Body`. Navigation goes `loop.NavigateMsg` → `app.admit` (`NavFree` / `NavConfirm` / `NavAllowlist`) → `app.navigate`.

## 4. Architecture

Form state lives in the DOM, focus lives in the sandbox, and text entry lives in the UI. Each piece is owned by the goroutine that already owns that layer.

```
 keys ──► app ──(browse mode)──► viewer (scroll, /search)        DOM focus = Sandbox.active
          │  Tab/Shift+Tab/Enter/Space ──► loop.FocusNext / loop.Activate
          │  click ──► loop.ClickAt (already exists)
          │
          │◄── loop.FocusMsg{Source, Control} ── loop (moves focus, fires focus/blur)
          │        app: viewer.FocusSource(id) (ring + scroll); text control + user action ⇒ edit mode
          │
          └─(edit mode)─► editor (bubbles textinput / textarea) ── each change ──► loop.SetValue(id, v)
                                                                      loop: dom state, input event, dirty frame
 submit: loop builds the form data set ──► NavigateMsg{Href, Form: true} ──► app.admit (unchanged gate)
```

- **`dom`** gains per-element form state: value, dirty-value flag, checkedness, dirty-checkedness flag, and option selectedness. It follows the HTML model: attributes are defaults, state is current. The serializer writes current state into the frame: `value="…"` on inputs, `checked` on checkboxes and radios, `selected` on options, and the current text as a `textarea`'s content. The tree's attributes are left alone, so `getAttribute('value')` still returns the default. The serializer also mints `nt-h-N` ids for every focusable element without an id, so each control and link box maps back to its node.
- **`jsvm`** gains the bindings in §6 and non-bubbling dispatch (`focus`, `blur`), plus `focusin`/`focusout`, which bubble.
- **`loop`** owns focus movement (it has the tree and the last `Result.Boxes`), activation (toggle, select, submit, reset, link), the form data set and submission. It reports focus changes to the app.
- **`app`** owns the input mode (browse / edit / choose), the editor widget, the focus ring through the viewer, and routes keys by mode.
- **nthtml** gets one small viewer API, `FocusSource(id)`, which draws the existing link-focus highlight around any source (§9). Until that ships, the app falls back to `NavigateSource` (scroll only) and a status-line indicator, so this round is not blocked on it.

## 5. Decisions

1. **One focus, owned by the DOM.** `Sandbox.active` is the single source of truth for links and controls alike. The viewer's own link focus is no longer used: the app intercepts Tab / Shift+Tab / Enter before the viewer sees them. Clicking a link still produces nthtml's `LinkMsg` and goes through `LinkActivated` as today. Script `focus()` moves the ring too, because `document.activeElement` and the ring must never disagree.
2. **Focus order** is document order over elements that are focusable (`focusable()`, minus `disabled` controls and `tabindex="-1"`) and visible, meaning they have a box in the latest `Result`. Positive `tabindex` values come first in ascending order, as in browsers, then `tabindex="0"` and natural order. Tab wraps from the last element to the first. Hidden elements are skipped because the order is computed against the frame on screen.
3. **Ids for hit-testing.** The serializer mints `nt-h-N` for every focusable element without an id, not only listener-bearing ones. `nthtml-control-N` therefore never appears for ntbrowser pages, and `Box.Anchor` resolves through `Tree.ByID` as clicks already do. The minted id is already budgeted (`elementOverhead` reserves it).
4. **Only the reader starts text entry.** A text control enters edit mode when the reader moves focus to it (Tab, click) or presses Enter on it. Script `focus()` (google.com focuses its search box on load) and `autofocus` move the ring and `activeElement` but leave the app in browse mode. Otherwise a page could capture `q`, `/` and the scroll keys the moment it loads.
5. **The editor is a terminal widget, not drawn in the page.** Edit mode replaces the status line with a one-line editor (`bubbles/textinput`) labelled with the field's accessible name: the `label` text, else `aria-label`, else `placeholder`, else `name`. A `textarea` opens a 6-row editor panel (`bubbles/textarea`) above the help line, and the viewer shrinks by those rows while it is open. Every change is posted to the loop (`SetValue`), so the page shows the new value in its next coalesced frame (`MinFrame`, 33 ms). Drawing a caret in the raster page would need nthtml selection and caret support. The terminal widget gives cursor movement, word deletion and paste for free.
6. **Keys in edit mode.**
   - Printable keys, editing keys and paste go to the editor.
   - **Enter:** in `input`, implicit submission (decision 10); in `textarea`, a newline.
   - **Tab / Shift+Tab:** commit the value and move focus.
   - **Esc:** commit and return to browse mode, with focus staying on the field.
   - **Ctrl+C:** still quits. `q` and `/` are text.
   - The page still gets its `keydown` copy, as today, only when it has a listener, but it cannot cancel text entry this round. Honouring `preventDefault` would make every keystroke wait on a round trip to the loop. Script that rewrites `value` on `input` (filters, formatters) still works through decision 16.
7. **Password fields** mask the value as `•` in the editor. nthtml already masks it in the page. Values are never logged, never put in the status line and never included in `LogMsg` errors.
8. **Checkbox and radio.** Space or Enter (browse mode), or a click: flip the checkbox, or check the radio and uncheck the others in its group (same `name`, same form owner, or no form). Then dispatch `click`. If `click` is prevented, restore the old state; otherwise dispatch `input` and `change`, as browsers do.
9. **Select is chosen in the status line.**
   - Enter or Space on a focused `select` opens a chooser in the status line showing the current option and `(i/n)`, with "↑↓ choose · Enter set · Esc cancel".
   - Set fires `input` and `change`. Disabled options and `optgroup` labels are skipped.
   - `multiple`: Space toggles the shown option, and Enter closes the chooser.
   - Option labels are page text and go through `app.clean`.
10. **Submission follows HTML's rules, GET only.**
    - **Implicit submission:** Enter in a text field submits through the form's first submit button, if it has one. Without one, it submits only if the form has exactly one field that blocks implicit submission, as the HTML spec says.
    - **Before submitting:** dispatch `submit` on the form. It is cancelable; `form.submit()` skips the event and `requestSubmit()` fires it.
    - **Form data set:** controls that have a `name`, are not disabled, and are not inside a disabled `fieldset` (except its first `legend`). Text-like fields and `textarea` contribute their value, with line breaks normalized to CRLF. Checked checkboxes and radios contribute their `value` (default `on`). Selected options contribute theirs. The submitter contributes its `name`/`value` if it has a name. `file` inputs contribute nothing in this round.
    - **URL:** encode as `application/x-www-form-urlencoded` (UTF-8). The action is `formaction` on the submitter, else the form's `action`, else the document URL, resolved against the page base. The encoded data replaces the action URL's query. `formmethod`/`method` must be `get`; `dialog` closes the enclosing dialog element without navigating.
11. **Submission takes the link path.** The loop sends `NavigateMsg{Href, Form: true}`, and `app.admit` applies `NavMode` unchanged: `NavAllowlist` blocks off-list targets, and `NavConfirm` asks before leaving the origin. The status line reads "Submit form to <host>? Enter · Esc" and shows the host, not the full URL with the values. `javascript:` and `data:` actions are refused, as unsafe links are.
12. **POST is refused visibly, for now.** A form whose effective method is `post` (or anything but `get`/`dialog`) does not submit, and the status line says "POST forms are not supported yet". Dispatching `submit` first is still correct, because many sites handle `submit` in script and never navigate. If a listener prevents it, nothing is shown.
13. **No GET submission of a non-empty password** (my call, stricter than browsers). A GET form containing a non-empty `password` field is not submitted, and the status line explains why. GET puts the value in the URL, in history and in server logs. Login forms are POST anyway, so this blocks almost nothing legitimate.
14. **Reset** (`type=reset` or `form.reset()`) restores every control's value, checkedness and selectedness from its defaults and clears the dirty flags. It dispatches `reset`, which is cancelable.
15. **Labels.** A click on a `label`, or Enter while a label's control is focused through it, activates the labelled control: the `for` attribute, else the first labelable descendant. The click is retargeted as in browsers. Focus moves to the control, and a checkbox or radio toggles.
16. **Script writes during editing.** If script sets `value` on the field being edited, the loop sends `ValueMsg{Source, Value}` and the editor replaces its text, keeping the cursor at the end. The value the reader sees is never stale.
17. **Bounds.** A value is capped at the control's `maxlength` (characters), and in any case at 64 KiB per control. State bytes are charged to the DOM budget like attribute bytes (`attrCost`). The editor refuses input past the cap. A form data set over 64 KiB, or a resulting URL over 16 KiB, is not submitted, and the status line says why.
18. **Cost on pages without forms is zero.** Focus events are dispatched only when listeners exist (`HasListeners`). A Tab press costs one tree walk over focusable elements against the cached boxes, and adds a frame only for the ring.

## 6. Interfaces (signatures the plan will pin)

```go
// dom: form state, current vs default (attributes stay defaults).
func (n *Node) Value() string                // current value (input, textarea, select's first selected option)
func (t *Tree) SetValue(n *Node, v string) error // marks dirty; charges budget; ErrBudget past caps
func (n *Node) Checked() bool
func (t *Tree) SetChecked(n *Node, on bool)  // radio group exclusivity is the caller's (loop/jsvm)
func (n *Node) Selected() bool               // <option>
func (t *Tree) SetSelected(opt *Node, on bool)
func (t *Tree) ResetControl(n *Node)         // back to defaults, clears dirty flags
func FormOwner(n *Node) *Node                // form attribute, else nearest <form> ancestor
func FocusOrder(t *Tree, visible func(*Node) bool) []*Node

// jsvm: events and bindings.
type Event struct {
	Type    string
	Target  dom.NodeID
	Key     string
	Href    string
	Bubbles bool // false for focus/blur; existing callers set true
}
func (s *Sandbox) SetFocus(id dom.NodeID) (blurred, focused dom.NodeID) // fires blur/focusout, focus/focusin
// Bindings: value, defaultValue, checked, defaultChecked, type, name, disabled, readOnly, form,
// selectedIndex, options (live array), option.selected/value/text, form.elements (array with
// namedItem), submit(), requestSubmit([submitter]), reset(). Value writes go through Tree.SetValue
// and notify the host (ValueChanged hook) so an open editor follows.

// loop: focus, activation, editing, submission.
func (l *Loop) FocusNext(backward bool)
func (l *Loop) Activate()                    // Enter/Space on the focused element (browse mode)
func (l *Loop) SetValue(source string, v string, final bool) // final=true fires change
func (l *Loop) Choose(source string, index int, toggle bool) // select chooser result

type (
	FocusMsg  struct{ Source string; Control ControlInfo } // Source "" = focus cleared
	ValueMsg  struct{ Source, Value string }
	ControlInfo struct {
		Kind        string // "", "text", "password", "textarea", "checkbox", "radio", "select", "button"
		Label       string // accessible name, page text: app cleans it
		Value       string
		MaxLength   int
		Options     []string // select labels; page text
		Selected    []int
		Multiple    bool
		ReadOnly    bool
	}
)
// NavigateMsg gains Form bool (status wording in admit; the gate is unchanged).
```

The app gains `mode` (browse, edit, choose) and the editor widgets. Key routing follows decision 6, and the help line shows the keys for the current mode.

## 7. Threat model additions

- **Keystroke capture:** only reader actions enter edit mode (decision 4). A page cannot type into or read the editor except through the value it already owns.
- **Exfiltration:** typed values leave only through a submission the reader triggered, through `admit`'s `NavMode` gate. GET with a password is refused (decision 13). `fetch()` from page script can send values the page reads, which is the same power a page has in any browser, and it is already constrained by the fetch policy.
- **Terminal injection:** labels, option text, placeholders and script-set values reach the editor and status line only through `app.clean`. The editor never renders raw page bytes.
- **Resource use:** value and form-data caps (decision 17), DOM budget charging, and one VM slice per dispatched event under the existing watchdog.
- **URL smuggling:** action URLs go through `document.ResolveURL`/`SafeURL` like links; `javascript:`/`data:` are refused.

## 8. Verification the plan must include

- `dom`: state vs attributes (`getAttribute('value')` keeps the default after typing); the serializer writes current state; minted ids for focusable elements without ids; budget charging and the 64 KiB cap; `ResetControl`; `FormOwner` with the `form` attribute.
- **Focus order:** document order, positive `tabindex` first, disabled and `tabindex=-1` skipped, invisible (`display:none`) skipped, wrap-around, and Shift+Tab.
- **Form data set:** text, password, textarea CRLF, checked and unchecked boxes, radio groups, select single and multiple, submitter name/value, disabled controls and disabled `fieldset` excluded, `formaction` override, query replacement.
- **Implicit submission:** with a submit button, without one and one field (submits), without one and two fields (does not submit).
- **Events and cancellation:** `click` prevented on a checkbox restores state; `submit` prevented means no `NavigateMsg`; `input` per change, `change` on commit; `focus`/`blur` do not bubble, `focusin`/`focusout` do; listener-free pages dispatch nothing (count VM entries).
- **Policy:** POST is refused with a message after `submit` has been dispatched; GET with a non-empty password is refused; `NavConfirm` asks with "Submit form to <host>?"; `NavAllowlist` blocks.
- **Script focus:** `focus()` on load moves the ring but not the input mode, so `q` in browse mode still quits.
- **app:** key routing per mode (q/`/` are text in edit mode; Ctrl+C quits; Esc returns to browse with focus kept); the editor follows a script write (`ValueMsg`); option labels and labels are cleaned.
- **End to end** (httptest): a page with a GET search form; Tab to the field, type `hello world`, Enter → `NavigateMsg` for `/search?q=hello+world`, then the results page loads. Plus an ntrecord run against google.com: Tab to the search box, type, Enter, and a results page renders.
- `GOOS=js GOARCH=wasm` build of the sandbox packages still passes; `jsvm.TestNoSystemImports` still holds.

## 9. Upstream request (nthtml, small)

`Model.FocusSource(id string) tea.Cmd` draws the existing link-focus highlight around the region `Result.SourceRect`-style lookup gives for `id` (boxes and text runs with that `Anchor`/`SourceID`) and scrolls it into view. `FocusSource("")` clears it. The host re-applies it after each `SetResult`, so nthtml needs no persistence. Until it ships, the app uses `NavigateSource` to scroll and shows "focus: <label>" in the status line. Shipped in nthtml v0.2.1 as `FocusSource` (on a focus move; scrolls only if the element is not fully visible) and `ShowFocusSource` (after each frame; never scrolls).

## 10. Calls most worth checking

- **Decision 1:** the viewer's own link focus is replaced by DOM focus, so Tab moves through links and controls together.
- **Decision 4:** script and `autofocus` focus never start text entry. google.com loads with its box focused but in browse mode; Tab or a click starts typing.
- **Decision 5:** typing happens in a terminal editor line (and a 6-row panel for `textarea`), with the page updating live, not in an in-page caret.
- **Decision 12:** POST is out of this round and shows a message.
- **Decision 13:** GET forms with a non-empty password are refused. That's stricter than browsers.

## 11. Left to the builder

The exact help-line wording per mode, the editor's styling, whether the select chooser wraps at the ends, how `FocusOrder` caches against a frame, and whether `app/cell.go` is replaced by the now-public `Model.CellToDocument` in this round or the next.
