# Usability survey: GOV.UK and APG

The strongest finding is **partial JavaScript support can make readable content
inaccessible**. GOV.UK's classic bootstrap applies `js-enabled`, but its ES
module enhancement never runs. At both widths, accordion body text disappears
and no sequentially focusable accordion controls exist. Conditional contact
fields in the checkbox and radio examples likewise disappear: the focus order
shrinks from six inputs to three.

This is a better next usability target than a broad API checklist. Preserve
content access when enhancement is unavailable, and make unsupported script
modes visible to the reader. Do not advertise module support merely to satisfy
feature detection. Automatic rollback of arbitrary page mutations would need
a separate design; a clear scripts-off reload is a smaller, testable remedy.

## Baseline and method

Measured against local ntbrowser commit `917f9eb` and published nthtml v0.6.0.
The newer sibling nthtml implementation is deliberately not substituted.
The runner used Go 1.27.4, `GOWORK=off CGO_ENABLED=0`; fixture revisions and
hashes are in [the manifest](corpus/manifest.json). See [reproduction and scope](README.md).

All 120 combinations produced an initial layout and TTY snapshot without a
render error. **This does not mean 120 usability passes.** The raw observations
are in [baseline.json](results/baseline.json). Loopback ports in diagnostics
are normalized to `http://fixture.local`.

The first pass records initial content, focus candidates and activation of
only the first candidate. It does not perform complete widget keyboard tasks
or visually inspect rendered pixels. An unchanged DOM does not imply an
unchanged native value; a changed DOM does not prove an intended outcome.

## Findings and next work

| Priority | Evidence | Classification | Next step |
| --- | --- | --- | --- |
| 1 | GOV.UK accordion: scripts off retains body text; scripts on retains only “Section A” and “Section B”, with no focus candidates. Conditional checkboxes/radios lose three dependent fields. Both widths reproduce it. | Unsupported modules combined with partial enhancement | Add a focused regression for content lost during bootstrap; design an explicit scripts-off recovery action and unsupported-module diagnostic. |
| 2 | APG manual tabs logs `unsupported selector "[role=tablist].manual"` in its load listener at both widths. | DOM selector compatibility | Add bounded compound attribute/class selector support and a task assertion that selects the second tab and exposes its panel. |
| 3 | APG dialog's only visible launch button causes no serialized DOM change with scripts enabled. Upstream uses `onclick="openDialog(...)"`. | Intentional inert event-handler attributes | Keep the boundary explicit. Decide separately whether a gated handler model belongs in the platform; do not silently rewrite the corpus to native listeners. |
| 4 | APG print button's click listener throws because `window.print` is absent. | Unsupported host action | Surface the unsupported action without adding printing authority as a compatibility shortcut. |
| 5 | APG button emits an SVG external/active-reference warning, even with scripts off. | Renderer restriction | Inspect the supplied SVG and whether a bounded inert subset can preserve its meaning; assess pixels before proposing a change. |

GOV.UK native details changes DOM on reader activation with scripts either on
or off at both widths. Seven APG examples show a DOM change on the first
activation when scripts are on: accordion, disclosure navigation, checkbox,
radio, menu button links, scrollable listbox and select-only combobox. These
are useful starting points for stronger assertions, not claims of complete
support. The APG checkbox and radio examples have no native script-free
interaction contract; their scripts-off snapshots are controls for comparison.

Next expand the probes into actual tasks: open/close disclosures; select a
second tab; use arrow keys and Escape in menus/listboxes; verify focus returns
after dismissal; reach error-summary targets; enter conditional contact data.
Capture before/after visible text and native control values, then visually
review narrow and wide layouts and focus indication. Error-summary links and
the skip-link need composed target context before judging their navigation;
the isolated upstream examples do not supply complete destinations. File
upload remains an intentional host-policy boundary in this profile.

## Harness checks

Fixture hashes and references are verified by the default integrity test.
The survey runs through the normal stylesheet subresource path using a local
server, with same-origin admission only. The initial attempt to inline the
full GOV.UK stylesheet hit the per-text-node DOM limit; that was a harness
artifact, not a platform finding. The final baseline uses the host stylesheet
loader and includes APG's imported syntax stylesheet, so no missing fixture
stylesheet warnings remain. No production code or dependency versions changed.
