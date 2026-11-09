# Glynx guide

Glynx is the browsing application built on the ntbrowser engine: a terminal
browser in the Lynx tradition that runs page JavaScript in a sandboxed Go
interpreter and draws the page with nthtml. This guide covers daily use. The
[README](../README.md) is the reference for every flag and security gate.

## Install

Build from source with `task build`; the binaries land in `bin/`. Release
archives on GitHub contain `glynx`, `ntbrowser` and the `ntbrowser-media`
helper; install all three into the same directory. The Homebrew cask
`nimbleterminal/tap/ntbrowser` does the same on macOS.

## Run

```sh
glynx                             # opens the address/search prompt
glynx https://github.com          # opens a page
glynx github.com                  # https:// is assumed
glynx "terminal browsers"         # words without a dot search Google
glynx --show-profile              # prints the effective permissions and exits
```

Pages are drawn as images in a terminal that supports the Kitty graphics
protocol (rex, kitty, Ghostty, WezTerm and others); elsewhere glynx falls back
to text cells. The terminal's pixel geometry sets the page width once known,
and `--width` sets the starting width.

## Keys

| Key | Action |
| --- | --- |
| `g` or `Ctrl+L` | Address and search prompt |
| `↑` / `↓` | Move focus to the previous or next link or control |
| `→` or `Enter` | Open the focused link, press the focused button |
| `←`, `⌫` or `⌥←` | Back |
| `f` or `⌥→` | Forward |
| `Page Up` / `Page Down` | Scroll a screen; `j`/`k` scroll a step, `h`/`l` pan sideways |
| `/` | Search the page |
| `Ctrl+R` | Reload |
| `Tab` / `Shift+Tab` | Move focus through links and form controls |
| `Space` | Toggle a focused checkbox or radio |
| `Alt+C` | Copy the status or error message to the clipboard (OSC 52) |
| `F12` | Read-only devtools, only when started with `--devtools` |
| `Esc` | Leave a full-view image, a prompt or an editor |
| `q` or `Ctrl+C` | Quit |

The mouse works too: clicks reach links and controls, and a link shows a cyan
outline under the pointer. A page can never swallow these keys.

**Address prompt.** Type a URL or a hostname and press `Enter`. Text with
spaces, or without any dot or slash, becomes a Google search. `Ctrl+U`
clears the text before the cursor and `Esc` cancels. Terminal paste works;
glynx never runs a clipboard helper.

**Forms.** `Tab` or a click on a text field opens an editor in the status line
(a six-row panel for a textarea). Inside it `Enter` submits, `Tab` moves on and
`Esc` returns to browsing. `Enter` on a select opens a chooser. GET forms
submit like links. POST forms, and GET forms carrying a filled-in password
field, are not submitted; the status line says why.

**Navigation prompts.** Links open freely. When a page's script tries to
navigate on its own, glynx asks first: `Enter` goes, `Esc` stays.

## What is kept between runs

Glynx is meant to be a daily driver, so by default it remembers two things:

- **Cookies that have an expiry.** Sign-ins and consent choices survive a
  restart. Session cookies, the ones without an expiry, are dropped at exit,
  exactly as a desktop browser drops them when it closes.
- **`localStorage`.** Site preferences such as theme or layout.

Everything else is memory-only: session storage, navigation history and the
page cache start empty on every run. There are no bookmarks yet.

The state lives in two owner-only files, created with mode 0600 in a directory
created with mode 0700:

| Platform | Directory |
| --- | --- |
| Linux and other Unix | `$XDG_DATA_HOME/glynx/`, else `~/.local/share/glynx/` |
| macOS | `$XDG_DATA_HOME/glynx/`, else `~/Library/Application Support/glynx/` |

| File | Contents |
| --- | --- |
| `cookies.dat` | Cookies with an expiry, at most 256 in total and 50 per domain, expiry capped at 400 days |
| `local-storage.dat` | `localStorage` for every origin, 1 MiB per origin and 8 MiB in total |

Both files are opened before the process locks itself down. After that the
sandboxed process holds only those two descriptors; it cannot open, read or
create any other file, and on macOS the kernel policy names exactly these two
paths. Writes happen at most once a second per store and once more at exit.
A write that fails is reported once in the status line and glynx keeps
working in memory. A file that will not parse starts its store empty rather
than failing the launch.

`--state-dir DIR` moves both files, for example to keep a work and a personal
profile apart:

```sh
glynx --state-dir ~/.local/share/glynx-work https://github.com
```

## Private runs

```sh
glynx --safe https://example.com
```

`--safe` makes the run memory-only. Cookies and `localStorage` still work for
the duration of the run, but nothing is read from or written to the state
files, so a `--safe` run neither sees your saved sign-ins nor leaves any
trace. `--persist=off` is the same thing spelled as a gate.

To go further, close more gates for one run. Each flag applies on top of the
glynx profile:

```sh
glynx --cookies=off --local-storage=off --session-storage=off URL   # no site state at all
glynx --scripts=off URL                                             # no JavaScript
glynx --profile reader URL                                          # ntbrowser's reader profile: no scripts, no cookies, confirm every link
```

`--profile` and `--profile-file` replace the glynx defaults entirely rather
than layering on them, so `--profile reader` is a strict reading mode.
`--show-profile` prints what any combination amounts to.

## Resetting saved state

Quit glynx, then delete either or both files:

```sh
rm ~/.local/share/glynx/cookies.dat          # sign out of everything
rm ~/.local/share/glynx/local-storage.dat    # forget site preferences
```

Use the macOS path from the table above there. The next run recreates the
files empty. Deleting while glynx is running also works, with one difference:
the running process keeps writing to the removed file, which disappears with
it at exit, so that run's new cookies and storage are lost too.

There is no per-site clearing yet. `document.cookie` writes from a page can
expire a cookie, so signing out on the site itself also works.

## Diagnostics

When a page renders wrong or a script fails, record a run:

```sh
glynx --diagnostics /tmp/glynx-debug.jsonl https://example.com
```

The path must not exist. The file holds the effective profile, request
outcomes, console output, script errors, the original HTML and the DOM
snapshots the renderer saw. It never holds cookies or storage contents, but
it does hold page content, so treat it as private. See
[Diagnostic capture](../README.md#diagnostic-capture) for the record kinds.

## Limits worth knowing

- **No ES modules.** `<script type="module">` and `import()` are skipped with
  a warning in the status line. Pages that need them show their fallback.
- **No XMLHttpRequest, WebSocket, Worker or WebAssembly.** A script that
  touches one stops with a warning; the rest of the page keeps working.
- **No downloads, popups or new windows.** Links open in the current view;
  non-displayable responses report their type and size and stay put.
- **Budgets.** Each page image is capped at 8 MiB encoded, each script entry
  at 50 ms of CPU, and the whole page at a bounded heap. A page that keeps
  overrunning its slices has its scripts disabled, with a message.
- **Google and reCAPTCHA** may still refuse the browser; permissive defaults
  remove setup friction but do not supply missing browser APIs.

## Switching to the explicit binary

`ntbrowser` is the same engine with the sandbox closed by default and a
smaller set of defaults open per profile. Use it to open a single page under
a policy you choose, or when you want memory-only state with no decisions to
make. The [ntbrowser guide](ntbrowser-guide.md) covers it.
