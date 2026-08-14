# bilibili-tui

A terminal UI for the video posts on your bilibili 动态 — what the uploaders you
follow on [bilibili.com](https://www.bilibili.com) have submitted, newest first.
Posts expand inline, and `o` renders one in carbonyl (`b` opens the browser).
The timeline comes from the same web-dynamic JSON API `t.bilibili.com` calls,
with your existing browser session, so there is no app to register and no API key
to apply for.

Video is the point of this one, so the real screen for it is `tui --web`: there
each post grows an inline player that streams through the server (see
[Watching](#watching) below).

```
bilibili-tui   unread · 18                      updated 14:32:07

  何同学 · 1.2万播放   我做了一个东西                                  2h
▌ 某某番剧 · 300万播放  [番剧] 第 3 话 出发                             5h

      某某番剧 · 300万播放

      https://www.bilibili.com/bangumi/play/ep123456
  …
```

## Requirements

- Go 1.26+ (to build from source)
- For login only: a Chromium-family browser (Brave, Chrome, Chromium, Edge, …).

## Quick start

This app ships as part of [`tui`](../../README.md), so the usual way in is
`tui bilibili`. To run it on its own from a source checkout:

```sh
make auth     # opens a browser; log into bilibili; saves the session to ~/.config/tui/env
make run      # build and launch the TUI
```

`make check` fetches one page of the 动态 timeline and prints the result, handy
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
| `tab`      | show the left uploader column (hidden by default) |
| `o`        | render the post in [carbonyl](https://github.com/genkio/carbonyl) in the terminal; `q` quits back |
| `O`        | same as `o` but with `--graphics` (kitty graphics protocol) |
| `b`        | open in the browser            |
| `R`        | refresh the timeline           |
| `?`        | toggle help                     |
| `q` / esc  | collapse an expanded post, else quit |

## Configuration

Secrets live only in the environment (never in a config file). Everything else
can go in `~/.config/bilibili-tui/config.toml` or be set via these environment
variables (shell exports win over `config.toml`):

| Variable              | Default    | Meaning                                    |
| --------------------- | ---------- | ------------------------------------------ |
| `BLTUI_COOKIE`        | (required) | the full `bilibili.com` browser cookie set |
| `BLTUI_MAX_VIDEOS`    | `40`       | video posts to fetch per refresh (paged, a dozen at a time) |
| `BLTUI_UNREAD_ONLY`   | `true`     | hide read posts on refresh; `false` keeps them greyed |
| `BLTUI_THEME`         | `auto`     | `auto` (match terminal), `light`, `dark`   |
| `BLTUI_REFRESH`       | off        | auto-refresh interval, e.g. `2m`; keep it slow |
| `BLTUI_UA`            | (built-in) | User-Agent override, to match the cookie's browser |

See `.env.sample` for a copy-paste template.

## Watching

bilibili hands out no stream a web page can use: the mp4 lives behind a play-URL
call, and its CDN refuses any request that does not carry a `bilibili.com`
Referer, which a page cannot forge. So the launcher's web view (`tui --web`)
points each card's player at its own `/bili?id=BV…` route, which looks the video
up, streams the bytes with the headers bilibili wants, and passes `Range` through
so seeking and the rotate-to-fullscreen player work like any other clip. `keep`
asks the same route for the file as a download.

With a session captured, that stream is normally 1080p; without one bilibili
only offers a low-quality preview. Play URLs expire within hours, so the route
re-resolves one that has gone stale on the next tap. A 番剧 episode names no
`BV…` id, so those cards stay a link out rather than growing a player.

## Authentication

`make auth` (or `tui bilibili --auth`) opens a Chromium-family browser on
`t.bilibili.com`, waits for you to finish logging in (QR code, SMS, or password —
whatever bilibili offers), then captures the `bilibili.com` cookie set (the
`SESSDATA` login cookie is the important one) into `~/.config/tui/env`.

When the session eventually expires, the app reports it as stale and the launcher
shows a red dot; re-run the same command to refresh it. bilibili also
risk-controls bursts of requests (`HTTP 412`, code `-352`); that one passes on its
own and re-auth won't help, so the app says so rather than sending you back
through a login.

## Read tracking

bilibili exposes no read state for the 动态 timeline, so bilibili-tui keeps one
locally: ids of posts you've seen live in
`~/.local/state/bilibili-tui/read.json` (capped, pruned oldest-first). The
launcher's merged "all" view flushes into the same store, so read state stays
consistent everywhere.
