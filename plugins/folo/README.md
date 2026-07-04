# folo-tui

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Built with Bubble Tea](https://img.shields.io/badge/built%20with-Bubble%20Tea-ff69b4)](https://github.com/charmbracelet/bubbletea)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A terminal UI for triaging your pending (unread) articles on
[Folo](https://app.folo.is) (the reader formerly known as Follow). It does one
job well: pull the Articles timeline, let you expand one inline to read it, open
it in the browser, and mark it read. Keyboard-driven, single binary, no browser
tab. It's the TUI of `https://app.folo.is/timeline/articles/all/pending`.

It authenticates by reusing your logged-in `folo.is` browser session cookie (the
same "cookie-stealth" idea as the other apps). `tui folo --auth` drives a
Chromium-family browser so you log in once and the cookie is captured
automatically.

```
+-----------+   HTTPS (web API + session cookie)   +-------------+
|  folo-tui |  <-------------------------------->   |  api.folo.is |
| (HTTP cl) |   POST /entries, /reads, GET /entries +-------------+
+-----------+
```

## How it works

Folo's web app (`app.folo.is`) talks to a JSON API (`api.folo.is`). folo-tui
calls the **same endpoints the website itself uses**, authenticated purely by
your browser session cookie:

- `POST /entries` with `{"view":0,"read":false}` lists the pending Articles.
- `GET /entries?id=…` fetches one entry's full body (the list omits it, so the
  body is loaded lazily the first time you expand an article).
- `POST /reads` marks entries read; `DELETE /reads` restores them to unread.

Because this rides the web API rather than a documented public one, it is
undocumented and **can break whenever Folo changes their API**. If listing or
reading stops working, that is the first place to look
(`internal/folo/client.go`).

## Features

- One screen listing your pending articles (newest first) with the source feed
  and a relative time.
- `space` expands the selected article inline; the body is fetched on demand,
  flattened from HTML to readable plain text, and the article is marked read.
  `q`, `esc`, or `space` again collapses it.
- While an expanded body is taller than the window, `j`/`k` scroll it line by
  line; once you reach the end, `j` moves on and collapses it.
- `o` renders the article in carbonyl inside the terminal (`q` quits back);
  `b` opens it in your browser.
- `r` marks the selected article read; on the next refresh it drops off, so you
  triage top to bottom.
- `K` keeps the selected article unread: moving past it no longer marks it read
  (and `r` is blocked) until `K` unlocks it. On an already-greyed article, `K`
  also marks it unread on the server, so it survives a refresh. The pins
  themselves live in memory only; a manual `R` refresh clears them.
- Optional auto-refresh, a "last updated" indicator, and a help bar.
- Your session cookie is never logged, printed, or written to disk by the app.

## Requirements

- **macOS or Linux.**
- **Go 1.26+** to build from source.
- For login: a Chromium-family browser (Brave, Chrome, Chromium, Edge, …). You
  can also set the cookie by hand instead.
- A Folo account you can log into.

## Install

Ships as part of [`tui`](../../README.md) (`brew install genkio/tap/tui`, then
`tui folo`). To build this app on its own from a source checkout:

```bash
cd plugins/folo
make build            # produces ./folo-tui
# or: make install    # into $GOBIN / $GOPATH/bin
```

## Authentication

folo-tui reads your browser session cookie from the `FOLO_COOKIE` environment
variable and sends it as the `Cookie` header. **It only reads the value to send
it; it is never stored or logged.**

### The easy way: `--auth`

```bash
tui folo --auth   # or `make auth` from a source checkout
```

This opens a Chromium-family browser with a dedicated persistent profile (so you
rarely have to log in again), waits for you to sign in, then saves the cookie as
`FOLO_COOKIE` to `~/.config/tui/env`. Re-run it whenever the session expires.

### The manual way

Prefer not to use a browser? Open
`https://app.folo.is/timeline/articles/all/pending` logged in, open DevTools
(`Cmd+Option+I` / `F12`) → Network, reload, click any `api.folo.is` request,
and copy the entire **`Cookie`** request header into `~/.config/tui/env`:

```bash
export FOLO_COOKIE='__Secure-better-auth.session_token=...; ...'
```

You can paste the bare value or the whole `Cookie: ...` line; the leading
`Cookie:` is stripped automatically. The cookie expires periodically and on
logout; re-capture it when auth starts failing.

## Quick start

```bash
tui folo --auth     # log in once (writes ~/.config/tui/env)
tui folo            # launch the TUI
# from a source checkout:  make auth && make run
```

## Keybindings

| Key | Action |
| --- | --- |
| `j` / `↓`, `k` / `↑` | Move down / up, marking the article you leave read; inside a long expanded article, scroll its body line by line |
| `space` | Expand / collapse the selected article inline; expanding fetches the body and marks it read |
| `o` | Render the article in [carbonyl](https://github.com/genkio/carbonyl) inside the terminal (adblock + vim keys); `q` quits back to the list |
| `O` | Same as `o` but rendered with `--graphics` (kitty graphics protocol) |
| `b` | Open the selected article's URL in your browser |
| `r` | Mark the selected article read |
| `K` | Keep unread: moving past won't mark it read, a greyed article is un-read on the server too; `K` again unlocks (pins cleared by `R`) |
| `q` / `esc` | Collapse the expanded article; on the bare list, `q` quits |
| `R` | Refresh the list |
| `g` / `G` | Jump to top / bottom |
| `tab` | Toggle the feed column |
| `?` | Toggle full help |
| `ctrl+c` | Quit |

## Configuration

Everything has a sensible default. To override, create
`~/.config/folo-tui/config.toml` (see
[`config.example.toml`](config.example.toml)) or pass `--config <path>`.
Environment variables win over the file.

### Settings vs. the cookie

- **Settings** (theme, refresh, unread-only, page size, hosts, user-agent) are
  read from `config.toml` automatically at startup, from any directory.
  Precedence: flag > `FOLO_TUI_*` env var > file > default.
- **The cookie** (`FOLO_COOKIE`) is a secret, so it is never stored in
  `config.toml`. It must be present in the environment when folo-tui runs.

| Variable | Default | Meaning |
| --- | --- | --- |
| `FOLO_COOKIE` | (required) | Browser session `Cookie` header |
| `FOLO_TUI_THEME` | `auto` | `auto` (match terminal background), `light`, or `dark` |
| `FOLO_TUI_UNREAD_ONLY` | `true` | Pending (unread) triage vs all articles |
| `FOLO_TUI_REFRESH` | (off) | Auto-refresh interval, e.g. `5m`. The `--refresh` flag overrides it. |
| `FOLO_TUI_MAX` | `50` | How many articles to fetch per load (newest first) |
| `FOLO_TUI_BASE_URL` | `https://api.folo.is` | Folo API host |
| `FOLO_TUI_WEB_URL` | `https://app.folo.is` | Folo web app origin (Origin/Referer) |
| `FOLO_TUI_USER_AGENT` | a Chrome string | User-Agent sent with requests |
| `XDG_CONFIG_HOME` | `~/.config` | Where the config file lives |

## Flags

- `--check` connect, fetch a few articles, and exit.
- `--version` print the version.
- `--config <path>` use a specific config file.
- `--refresh <duration>` auto-refresh on an interval, e.g. `--refresh 5m`.

## Troubleshooting

- **"Folo rejected the session"** the cookie is missing or expired. Run
  `make auth` again (or re-copy `FOLO_COOKIE`).
- **`--check` says 0 fetched** you may be at inbox zero. Try
  `FOLO_TUI_UNREAD_ONLY=false` to view all articles.
- **Expanded text is sparse or empty** some feeds carry only a summary; press
  `o` to read the full article in carbonyl (or `b` in the browser).
- **Listing/reading stops working** Folo likely changed their API; the client in
  `internal/folo/client.go` needs updating. Press `o` (carbonyl) or `b`
  (browser) to read meanwhile.

## Security notes

- This tool sends your session cookie to Folo over HTTPS and nowhere else. It is
  never logged, printed, or written to disk by folo-tui.
- Reusing the browser session is a gray area under Folo's terms; you are the
  only user and it touches only your own account. Its only writes are mark-read
  and mark-unread, through the web app's own endpoints.
- Never commit your cookie. Creds live in `~/.config/tui/env` (0600). The login
  browser profile lives under `~/.config/tui/profile` and also holds your
  session; keep it private.

## License

MIT. See [`LICENSE`](LICENSE).

## Acknowledgements

- Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
  [Bubbles](https://github.com/charmbracelet/bubbles), and
  [Lip Gloss](https://github.com/charmbracelet/lipgloss).
- Browser login via [chromedp](https://github.com/chromedp/chromedp).
- For [Folo](https://github.com/RSSNext/Folo), the open-source feed reader.
