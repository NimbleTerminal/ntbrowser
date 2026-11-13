# Local usability corpus

Thirty pinned examples: 20 GOV.UK Frontend components and 10 W3C ARIA
Authoring Practices Guide (APG) widgets. These exercise forms, disclosures,
validation, navigation, composite widgets and dialogs. This is a diagnostic
survey, not an accessibility certification or upstream conformance suite.

## Reproduce

Run from the repository root with the published nthtml dependency in go.mod:

```sh
GOWORK=off CGO_ENABLED=0 go test ./loop -run TestUsabilityCorpusIntegrity -count=1
GOWORK=off CGO_ENABLED=0 NTBROWSER_USABILITY_REPORT=/tmp/usability-results.json go test ./loop -run '^TestUsabilitySurvey$' -count=1 -timeout=10m
```

The survey runs 120 combinations: 30 examples, 320/800 CSS pixels and
scripts off/on. A locked profile enables same-origin subresources from a loopback-only fixture
server and inline classic scripts for the on variant. Page fetch, external
origins, file access and navigation remain closed. This is
not the glynx profile. Each fresh page produces initial TTY text, layout box
count, script diagnostics, sequential focus candidates, and whether focusing
and activating the first candidate changes serialized DOM. Layout success
and DOM changes are observations, not widget success assertions. Native
control state can change without changing serialized markup.

The first-frame snapshot and owner-queue barrier do not await arbitrary
upstream timers. This pass does not measure pixel fidelity, focus-ring
visibility, keyboard arrow/Escape handling, complete task completion, screen
reader announcements, live network behavior or file selection. Relative
navigation targets may intentionally lie outside the isolated fixture.

## Provenance and transformations

- [GOV.UK Frontend](https://github.com/alphagov/govuk-frontend/tree/a11bd3316363ab4702fcef81d6ce06a947c464ef), version 6.6.0, MIT (`corpus/govuk/LICENSE.txt`).
- [W3C APG](https://github.com/w3c/aria-practices/tree/f14b2490a6728cda85fd7feafbd148b3c3eaf038), upstream licensing in `corpus/apg/LICENSE.md`.

`corpus/manifest.json` records source paths, GOV.UK YAML example names,
revisions, asset references and SHA-256 for every vendored file. HTML is
transformed; JS and CSS files are copied byte-for-byte. GOV.UK templates are
rendered with pinned Nunjucks and js-yaml versions. APG markup is extracted
between its example separator markers; documentation-only scripts and the
remote W3C documentation stylesheet are omitted. Shared core CSS and widget
helpers are retained. The harness serves CSS through the normal subresource path and embeds script
bytes in a document wrapper, preserving module types and inline event attributes. Module scripts
remain unsupported; inline event attributes remain inert. Font/image URLs
in CSS are not fetched. No polyfills or script rewrites conceal missing APIs.

To regenerate, download and extract these immutable archives outside the repo:

```
https://codeload.github.com/alphagov/govuk-frontend/tar.gz/a11bd3316363ab4702fcef81d6ce06a947c464ef
SHA256 cb875bb66fd0c806fe4f975fbc17ef567965739d67d69c7039b3695f1c239a50
https://codeload.github.com/w3c/aria-practices/tar.gz/f14b2490a6728cda85fd7feafbd148b3c3eaf038
SHA256 7893ab45b8cf1957a77b85575ac2e790f029f6bb933fb4b41777f52aaf46268a
```

```sh
cd internal/usability/tools
npm ci --ignore-scripts --no-audit --no-fund
node generate.mjs /path/to/govuk-frontend-REV /path/to/aria-practices-REV
```

The corpus stays local; regeneration and surveying require no publication,
tag, push, sibling module replacement or dependency release.
