# inoreader-tui

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Built with Bubble Tea](https://img.shields.io/badge/built%20with-Bubble%20Tea-ff69b4)](https://github.com/charmbracelet/bubbletea)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A terminal UI for triaging your unread [Inoreader](https://www.inoreader.com).
It does one job well: pull your unread articles, show them oldest first, let you
expand one inline to read it, open it in the browser, and mark it read.
Keyboard-driven, single binary, no browser tab.

It authenticates by reusing your logged-in `inoreader.com` browser session
cookie, the same "stealth" idea as
[slack-tui](https://github.com/genkio/slack-tui). A bundled helper drives a real
browser with [`playwright-cli`](https://github.com/microsoft/playwright) so you
just log in once and the cookie lands in `.env` automatically.

```
+----------------+   HTTPS (web "xajax" API)    +-----------+
|  inoreader-tui |  <------------------------>  | Inoreader |
|  (HTTP client) |   /?xjxfun=... + cookie      |    web    |
+----------------+                              +-----------+
```

## How it works (and the catch)

Inoreader's clean, documented [Reader API](https://www.inoreader.com/developers/)
(`/reader/api/0/...`) requires a registered developer `AppId`, which is gated
behind a paid plan. So instead this tool calls the **same internal endpoints the
website itself uses** (`/?xjxfun=print_articles`, `/?xjxfun=read_article`),
authenticated purely by your session cookie. No registration, no plan, works on
free accounts.

The catch: those endpoints return the website's own HTML, so inoreader-tui
**scrapes** each article's title, link, feed, author, and body out of that
markup. That works well today, but it is undocumented and **can break whenever
Inoreader changes their web front end**. If articles stop parsing, that is the
first place to look (`internal/inoreader/client.go`, `scrapeArticle`).

## Features

- One screen listing your unread articles, oldest first, with the source feed
  and a relative time.
- `space` expands the selected article inline to its full text (HTML flattened
  to readable plain text) and marks it read; `q`, `esc`, or `space` again
  collapses it.
- While an expanded body is taller than the window, `j`/`k` scroll it line by
  line; once you reach the end, `j` moves on and collapses it.
- `o` renders the article in carbonyl inside the terminal (`q` quits back);
  `b` opens it in your browser.
- `r` marks the selected article read; the list refreshes and it drops off, so
  you triage top to bottom.
- `K` keeps the selected article unread: moving past it no longer marks it read
  (and `r` is blocked) until `K` unlocks it. Kept articles stay in normal
  colors. On an already-greyed article, `K` also marks it unread on the server,
  so it is still there after a refresh. The pins themselves live in memory
  only; a manual `R` refresh clears them.
- Optional auto-refresh, a "last updated" indicator, and a help bar.
- Your session cookie is never logged, printed, or written to disk by the app.

## Requirements

- **macOS or Linux.**
- **Go 1.25+** to build from source.
- For `make auth`: **[playwright-cli](https://github.com/microsoft/playwright)**
  (`npm i -g @playwright/cli` or your package manager) and a browser it can
  drive. You can also set the cookie by hand without it.
- An Inoreader account you can log into. Any plan works.

## Install

```bash
git clone https://github.com/genkio/inoreader-tui
cd inoreader-tui
make build            # produces ./inoreader-tui
# or: make install    # into $GOBIN / $GOPATH/bin
```

## Authentication

inoreader-tui reads your browser session cookie from the `INOREADER_COOKIE`
environment variable and sends it as the `Cookie` header. **It only reads the
value to send it; it is never stored or logged.**

### The easy way: `make auth`

```bash
cp .env.sample .env   # .env is gitignored
make auth             # opens a browser; log in, then press Enter
```

`make auth` opens a real browser window (persistent profile, so you rarely have
to log in again), waits for you to sign in, then writes the cookie to `.env` as
`INOREADER_COOKIE`. Re-run it whenever the session expires.

Under the hood it runs [`tools/auth.sh`](tools/auth.sh), which is reusable for
other sites:

```bash
tools/auth.sh <session> <url> <cookie-domain> <ENV_VAR> <env-file>
```

### The manual way

If you do not want playwright-cli: open `https://www.inoreader.com/all_articles`
logged in, open DevTools (`Cmd+Option+I` / `F12`) -> Network, reload, click any
`www.inoreader.com` request, and copy the entire **`Cookie`** request header.
Put it in `.env`:

```bash
export INOREADER_COOKIE='ssid=...; ...'
```

You can paste the bare value or the whole `Cookie: ...` line; the leading
`Cookie:` is stripped automatically. The cookie expires periodically and on
logout; re-capture it when auth starts failing.

## Quick start

```bash
make auth                    # capture the cookie into .env
source .env && inoreader-tui --check   # verify the connection
source .env && inoreader-tui           # launch the TUI
# or simply:  make run
```

## Keybindings

| Key | Action |
| --- | --- |
| `j` / `↓`, `k` / `↑` | Move down / up, marking the article you leave read; inside a long expanded article, scroll its body line by line |
| `space` | Expand / collapse the selected article inline; expanding marks it read |
| `o` | Render the article in [carbonyl](https://github.com/genkio/carbonyl) inside the terminal (adblock + vim keys); `q` quits back to the list |
| `O` | Same as `o` but rendered with `--graphics` (kitty graphics protocol) |
| `b` | Open the selected article's URL in your browser |
| `r` | Mark the selected article read (it drops off the list) |
| `K` | Keep unread: moving past won't mark it read, a greyed article is un-read on the server too; `K` again unlocks (pins cleared by `R`) |
| `q` / `esc` | Collapse the expanded article; on the bare list, `q` quits |
| `R` | Refresh the list |
| `g` / `G` | Jump to top / bottom |
| `?` | Toggle full help |
| `ctrl+c` | Quit |

## Configuration

Everything has a sensible default. To override, create
`~/.config/inoreader-tui/config.toml` (see
[`config.example.toml`](config.example.toml)) or pass `--config <path>`.
Environment variables win over the file.

### Settings vs. the cookie

- **Settings** (theme, refresh, unread-only, page size, base URL, user-agent)
  are read from `config.toml` automatically at startup, from any directory.
  Precedence: flag > `INOREADER_TUI_*` env var > file > default.
- **The cookie** (`INOREADER_COOKIE`) is a secret, so it is never stored in
  `config.toml`. It must be present in the environment when inoreader-tui runs.

| Variable | Default | Meaning |
| --- | --- | --- |
| `INOREADER_COOKIE` | (required) | Browser session `Cookie` header |
| `INOREADER_TUI_THEME` | `auto` | `auto` (match terminal background), `light`, or `dark` |
| `INOREADER_TUI_UNREAD_ONLY` | `true` | Unread-only triage vs the full "All articles" view |
| `INOREADER_TUI_REFRESH` | (off) | Auto-refresh interval, e.g. `5m`. The `--refresh` flag overrides it. |
| `INOREADER_TUI_MAX` | `50` | How many articles to fetch per load (oldest first) |
| `INOREADER_TUI_BASE_URL` | `https://www.inoreader.com` | Inoreader site root |
| `INOREADER_TUI_USER_AGENT` | a Chrome string | User-Agent sent with requests |
| `XDG_CONFIG_HOME` | `~/.config` | Where the config file lives |

## Flags

- `--check` connect, fetch a few articles, and exit.
- `--version` print the version.
- `--config <path>` use a specific config file.
- `--refresh <duration>` auto-refresh on an interval, e.g. `--refresh 5m`.

## Troubleshooting

- **"Inoreader rejected the session"** the cookie is missing or expired. Run
  `make auth` again (or re-copy `INOREADER_COOKIE`).
- **`--check` says 0 fetched** you may be at inbox zero. Try
  `INOREADER_TUI_UNREAD_ONLY=false` to view all articles.
- **Articles stop parsing / look blank** Inoreader likely changed their web
  markup; the scraper in `internal/inoreader/client.go` needs updating. Press
  `o` (carbonyl) or `b` (browser) to read meanwhile.
- **Expanded text is sparse** some feeds carry only a summary; press `o` (or
  `b`) for the full article.

## Security notes

- This tool sends your session cookie to Inoreader over HTTPS and nowhere else.
  It is never logged, printed, or written to disk by inoreader-tui.
- Reusing the browser session is a gray area under Inoreader's terms; you are
  the only user and it touches only your own account. Its only write is
  mark-as-read, through the web app's own endpoint.
- Never commit your cookie. `.env` is gitignored; `.env.sample` ships a
  placeholder. The playwright-cli browser profile lives under
  `~/.config/inoreader-tui/pw-profile` and also holds your session; keep it
  private.

## License

MIT. See [`LICENSE`](LICENSE).

## Acknowledgements

- Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
  [Bubbles](https://github.com/charmbracelet/bubbles), and
  [Lip Gloss](https://github.com/charmbracelet/lipgloss).
- HTML parsing via [`golang.org/x/net/html`](https://pkg.go.dev/golang.org/x/net/html).
- Cookie capture via [`playwright-cli`](https://github.com/microsoft/playwright).
