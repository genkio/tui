# x-tui

A terminal UI for reading your x.com (Twitter) home timelines. It lists the
**For You** and **Following** feeds, `tab` switches between them, posts expand
inline, and `o` opens one in the browser. It talks straight to the same web
GraphQL API the site uses, authenticated with your logged-in browser session,
so there is no app to register and no API plan to pay for.

```
x-tui   For You · Following  (82)                      updated 14:32:07

  @xicilion   以前当一个人说，我有一个点子，只缺一个程序员…            2h
▌ @patio11    A thing I've learned about pricing pages over the years…    3h

      Patrick McKenzie @patio11

      A thing I've learned about pricing pages over the years is that
      the anchor matters more than the number…

      42 replies · 310 reposts · 2.1K likes

      https://x.com/patio11/status/1875…

  @dhh        Reposted by someone you follow                              5h
  …
```

## Requirements

- Go 1.25+
- For `make auth` only: [`playwright-cli`](https://github.com/microsoft/playwright),
  `jq`, and `node` on your `PATH`.

## Quick start

```sh
make auth     # opens a browser; log into x.com; captures the session to .env
make run      # build and launch the TUI (sources .env)
```

`make check` fetches one page from each timeline and prints the result, handy
for confirming the session works before opening the UI.

## Keys

| Key        | Action                          |
| ---------- | ------------------------------- |
| `j` / `k`  | move down / up (scrolls a long expanded post line by line) |
| `g` / `G`  | jump to top / bottom            |
| `space`    | expand / collapse the post      |
| `←` / `→`  | switch For You / Following      |
| `tab`      | toggle the left `@handle` column |
| `o`        | open the post in the browser    |
| `R`        | refresh the current timeline    |
| `?`        | toggle help                     |
| `q` / esc  | collapse an expanded post, else quit |

## Configuration

Secrets live only in the environment (never in a config file). Everything else
can go in `~/.config/x-tui/config.toml` or be set via these environment
variables (shell exports win over `config.toml`):

| Variable           | Default     | Meaning                                   |
| ------------------ | ----------- | ----------------------------------------- |
| `XTUI_AUTH_TOKEN`  | (required)  | the `auth_token` session cookie           |
| `XTUI_CT0`         | (required)  | the `ct0` token (cookie + `x-csrf-token`) |
| `XTUI_DEFAULT_TAB` | `following` | tab to open on: `foryou` or `following`   |
| `XTUI_MAX_TWEETS`  | `50`        | posts to fetch per tab                    |
| `XTUI_THEME`       | `auto`      | `auto` (match terminal), `light`, `dark`  |
| `XTUI_REFRESH`     | off         | auto-refresh interval, e.g. `2m`; keep it slow |
| `XTUI_LANG`        | `en`        | `x-twitter-client-language`               |
| `XTUI_BEARER`      | built-in    | override the public web bearer (rarely needed) |

See `.env.sample` for a copy-paste template.

## Authentication

x.com's web GraphQL API authenticates with two values from a logged-in browser:

- `auth_token`: an `HttpOnly` session cookie.
- `ct0`: a CSRF token, sent both as the `ct0` cookie and the `x-csrf-token`
  header.

The OAuth2 `Bearer` is a public constant baked into the web app (identical for
every user), so it is not a secret and ships in the binary.

`make auth` runs `tools/auth.sh`, which opens a real browser with a persistent
profile (so re-login is rare), waits for you to log in, and writes
`XTUI_AUTH_TOKEN` and `XTUI_CT0` to `.env` via `playwright-cli`. Re-run it when
the session expires (the TUI says "x.com rejected the session" when that
happens).

Prefer to do it by hand? In your browser's DevTools, copy the `auth_token` and
`ct0` cookie values for `x.com` and set the two variables above.

## How it works

Each tab is one GraphQL read: `GET /i/api/graphql/<queryId>/HomeTimeline` (For
You) or `HomeLatestTimeline` (Following), with the session cookie, the public
bearer, and `x-csrf-token`. The response is a nested timeline of post entries
that the client flattens into a simple list.

The `queryId`s and the `features` flag set are lifted from the live web app and
**rotate when x.com redeploys**. If a timeline starts failing with "unknown
query id" or "feature ... must be defined", re-capture them from the network
panel and update the constants at the top of `internal/x/client.go`.

## Caveats

- **Account safety.** Automating x.com with your own session is against their
  terms, and x.com runs aggressive bot detection. For a personal, read-only
  tool at human pace the risk is low but not zero. Keep `XTUI_REFRESH` slow or
  off, and avoid bursts of `R`.
- **No mark-as-read.** A home timeline has no unread state, so the list is just
  a snapshot you scroll; `R` pulls a fresh one.
- Media-only posts (just an image or video) show as `[media]`; press `o` to
  open them.

## Development

```sh
make build    # ./x-tui
make test     # go test ./...
make lint     # fmt + vet + test
```
