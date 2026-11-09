# ntbrowser guide

`ntbrowser` opens one page under a security profile you choose. It is the
same engine as [glynx](glynx-guide.md), with every gate out of the sandbox
closed unless the profile opens it, and no state kept between runs. Use it to
read a page you do not trust, to run a small web application under a known
policy, or to reproduce a problem under exact settings. The
[README](../README.md) is the reference for every flag and gate.

## Install

Build from source with `task build`; the binaries land in `bin/`. Release
archives on GitHub contain `ntbrowser`, `glynx` and the `ntbrowser-media`
helper; install all three into the same directory, since the main executable
doubles as the Linux confinement stage and the helper decodes video. The
Homebrew cask `nimbleterminal/tap/ntbrowser` does the same on macOS.

## Run

```sh
ntbrowser https://example.com                  # browse profile: inline scripts, free links
ntbrowser --profile reader https://example.com # no scripts, confirm every link
ntbrowser --profile locked file:///tmp/page.html   # only the start page, nothing else
ntbrowser                                      # opens the address prompt
ntbrowser --show-profile --profile reader      # print the effective gates and exit
```

A `file://` URL is allowed only as the start page and must be given on the
command line, before the process locks itself down.

## Pick a profile

| Profile | Scripts | Links | Images and CSS | Cookies and storage |
| --- | --- | --- | --- | --- |
| `locked` | off | off: only the start URL loads | off | off |
| `reader` | off | confirm each one | page origin only | off |
| `browse` (default) | inline only | free | any public origin | off |

Every profile is memory-only. Nothing is written to disk and nothing is read
from the glynx state files; closing ntbrowser forgets cookies, storage and
history. `--show-profile` prints each gate with how it is held: `os` means the
kernel enforces it, `checked` means ntbrowser's own code refuses, and
`compiled-out` means the code is not in the binary.

On macOS the process re-executes itself under `sandbox-exec`; on Linux it uses
Landlock and seccomp. The `locked` profile exits if the operating system
cannot enforce it; the others run and report unenforced gates as `checked`.

## Adjust a profile

Flags open or close individual gates on top of the chosen profile:

```sh
ntbrowser --scripts=external --script-sources=any URL   # run the page's external scripts
ntbrowser --cookies=session URL                          # cookies for this run only
ntbrowser --nav=allowlist --allow=example.com,.example.org URL
ntbrowser --media=video URL                              # play a video link through mpv
ntbrowser --allow-private http://localhost:8080/        # development server
```

A gate never opens by accident: `--allow` lists hosts for fetch and
navigation but grants nothing to scripts, `--allow-script` grants a script
origin but widens no fetch access, and `--profile` replaces the defaults
rather than inheriting glynx's.

For a policy you use repeatedly, write a JSON profile. The fields are the gate
names from `--show-profile`; `base` names a built-in to start from, and
unknown fields are errors:

```json
{
  "base": "reader",
  "scripts": "inline",
  "cookies": "session",
  "hosts": ["docs.example.com"]
}
```

```sh
ntbrowser --profile-file ~/.config/ntbrowser/docs.json https://docs.example.com
```

To let a JSON profile keep cookies and `localStorage` across runs the way
glynx does, add `"local-storage": "on"` and `"persist": "file"`; the files
then live under `$XDG_DATA_HOME/ntbrowser/` or `--state-dir`. The
[glynx guide](glynx-guide.md#what-is-kept-between-runs) describes the files.

## Keys

| Key | Action |
| --- | --- |
| `Tab` / `Shift+Tab` | Move focus through links and form controls |
| `Enter` | Open the focused link, press the focused button |
| `Space` | Toggle a focused checkbox or radio |
| `⌫` or `⌥←` | Back |
| `⌥→` | Forward |
| `Page Up` / `Page Down` | Scroll a screen; `j`/`k` scroll a step, `h`/`l` pan sideways |
| `/` | Search the page |
| `Ctrl+L` | Address prompt |
| `Ctrl+R` | Reload |
| `Alt+C` | Copy the status or error message to the clipboard (OSC 52) |
| `F12` | Read-only devtools, only when started with `--devtools` |
| `Esc` | Leave a full-view image, a prompt or an editor |
| `q` or `Ctrl+C` | Quit |

Mouse clicks reach links and controls. Forms, the address prompt and
navigation confirmations behave as in the glynx guide: text fields open an
editor in the status line, GET forms submit through the navigation gate, POST
forms and password forms are not sent, and a `confirm` gate asks before each
link with `Enter` to go and `Esc` to stay.

## Diagnostics

```sh
ntbrowser --diagnostics /tmp/ntbrowser-debug.jsonl --scripts=external URL
```

The path must not exist; the file is created owner-only before lockdown and
holds the effective profile, request outcomes, console output, script results,
the original HTML and DOM snapshots. It contains page content, so treat it as
private. For a reproducible run, add `--seed N` to fix `Math.random` and
`crypto` values; the clock is never fixed. See
[Diagnostic capture](../README.md#diagnostic-capture) for the record kinds.

## What is not there

ntbrowser is a reader and small-application runtime, not a general browser:
no ES modules, XMLHttpRequest, WebSocket, Workers or WebAssembly; no downloads,
popups or new windows; no bookmarks or history across runs. A page that needs
one of these shows its fallback or a warning in the status line, and the rest
of the page keeps working. For day-to-day browsing use glynx, which opens the
same gates by default that this binary makes you open by hand.
