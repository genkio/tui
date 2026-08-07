# reddit-tui

A terminal UI for reading your Reddit home timeline — the personalized front
page Reddit shows when you're logged in (your subscribed `r/…`). Posts expand
inline, and `o` renders one in carbonyl (`b` opens the browser). It talks
straight to Reddit's classic JSON API the way the legacy web client does,
authenticated with your logged-in browser session, so there is no app to
register and no API plan to sign up for.

```
reddit-tui   unread · 37                       updated 14:32:07

  r/golang      Go 1.26 ships with a smaller runtime                    2h
▌ r/programming Why are we still writing C in 2026                       3h

      r/programming · @bob

      A long argument about memory safety, best served with
      moderation and a big grain of salt…  [(no text; o to read in
      carbonyl, b for browser)]

      https://example.com/c-article
  …
```

## Requirements

- Go 1.26+ (to build from source)
- For login only: a Chromium-family browser (Brave, Chrome, Chromium, Edge, …).

## Quick start

This app ships as part of [`tui`](../../README.md), so the usual way in is
`tui reddit`. To run it on its own from a source checkout:

```sh
make auth     # opens a browser; log into reddit; saves the session to ~/.config/tui/env
make run      # build and launch the TUI
```

`make check` fetches one page of the home timeline and prints the result, handy
for confirming the session works before opening the UI.

## Keys

| Key        | Action                          |
| ---------- | ------------------------------- |
| `j` / `k`  | move down / up (marks the post you leave read; scrolls a long expanded post line by line) |
| `g` / `G`  | jump to top / bottom            |
| `space`    | expand / collapse the post (expanding marks it read) |
| `r`        | mark the post read              |
| `K`        | keep the post unread (scrolling won't mark it; `K` again unlocks) |
| `u`        | toggle unread-only vs show-all (read posts greyed) |
| `tab`      | show the left `r/…` column (hidden by default) |
| `o`        | render the thread or article in [carbonyl](https://github.com/genkio/carbonyl) in the terminal; `q` quits back |
| `O`        | same as `o` but with `--graphics` (kitty graphics protocol) |
| `b`        | open in the browser            |
| `R`        | refresh the timeline           |
| `?`        | toggle help                     |
| `q` / esc  | collapse an expanded post, else quit |

## Configuration

Secrets live only in the environment (never in a config file). Everything else
can go in `~/.config/reddit-tui/config.toml` or be set via these environment
variables (shell exports win over `config.toml`):

| Variable           | Default     | Meaning                                   |
| ------------------ | ----------- | ----------------------------------------- |
| `RDTUI_COOKIE`     | (required)  | the full `reddit.com` browser cookie set  |
| `RDTUI_MAX_POSTS`  | `50`        | posts to fetch per refresh                |
| `RDTUI_UNREAD_ONLY`| `true`      | hide read posts on refresh; `false` keeps them greyed |
| `RDTUI_THEME`      | `auto`      | `auto` (match terminal), `light`, `dark`  |
| `RDTUI_REFRESH`    | off         | auto-refresh interval, e.g. `2m`; keep it slow |

See `.env.sample` for a copy-paste template.

## Read tracking

Reddit exposes no read state through its JSON API, so reddit-tui keeps one
locally, the way an RSS reader does. A post is marked read when you scroll past
it (`j`/`k`), expand it (`space`), or press `r`; read posts render greyed. `K`
keeps a post unread so scrolling won't touch it (`K` again unlocks).

Marks persist to `~/.local/state/reddit-tui/read.json`
(`$XDG_STATE_HOME/reddit-tui/read.json` if set), so a post you already saw stays
read across refreshes and restarts. In the default **unread-only** mode read
posts grey out in place and drop off on the next refresh, leaving only what's
new; press `u` to switch to showing everything with read posts merely greyed.
Set `RDTUI_UNREAD_ONLY=false` to make show-all the default.

The store is capped to the most recent 20,000 ids and pruned automatically;
deleting the file just resets everything to unread.

## Authentication

Reddit's classic JSON endpoint authenticates with the same cookies your browser
holds when you're logged in — the `reddit_session` cookie (with the rest of the
reddit.com cookie set) identifies you to `old.reddit.com/*.json`.

`tui reddit --auth` (or `make auth` from a checkout) opens a Chromium-family
browser with a dedicated persistent profile (so re-login is rare), waits for
you to log in, and saves `RDTUI_COOKIE` (the whole cookie set, joined as a
`Cookie` header) to `~/.config/tui/env`. Re-run it when the session expires (the
TUI says "reddit rejected the session" then).

Prefer to do it by hand? Log in to reddit.com in a browser, then build the
cookie header from the site's cookies: `reddit_session=…; token_v2=…; …` and set
`RDTUI_COOKIE` to that.

## How it works

Each fetch is one GET to `https://old.reddit.com/.json?limit=N&sort=new` (the
root home feed, sorted newest-first), sent with the session's `Cookie` header and
a browser User-Agent (Reddit rejects bare programmatic agents). The endpoint
returns a `Listing` whose `children` are the posts of your authenticated home
feed; the client keeps only the `t3` post entries and flattens each into a
simple row. `sort=new` is a stable reverse-chronological page, rather than
Reddit's default "best" which reshuffles between fetches.

A link post opens its external article; a self post (or one hosted on Reddit,
like an image or gallery) opens the thread on `old.reddit.com`, which renders
lightly in carbonyl and honors the session cookie too.

## Caveats

- **Account safety.** Automating Reddit with your own session is against their
  terms, and Reddit rate-limits both per-user and per-IP. For a personal,
  read-only tool at human pace the risk is low but not zero. Keep
  `RDTUI_REFRESH` slow or off, and avoid bursts of `R`.
- **Read state is local only.** Reddit has no unread concept in this API, so
  marks live in a file on your machine (see [Read tracking](#read-tracking));
  they don't sync to Reddit or other devices. `R` pulls a fresh timeline and
  re-applies them.
- Image/video posts have no text body; press `O` to view them in carbonyl with
  kitty graphics, or `b` in the browser.

## Development

```sh
make build    # ./reddit-tui
make test     # go test ./...
make lint     # fmt + vet + test
```
