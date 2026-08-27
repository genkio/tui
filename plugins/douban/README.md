# douban-tui

A terminal UI for reading your Douban following timeline (友邻广播) — the
stream of statuses, reshares, and 看过/在读/想看 marks from the people you
follow on [douban.com](https://www.douban.com), with the weekly 榜单 charts
mixed in. Statuses expand inline, and `o` renders one in carbonyl (`b` opens
the browser). The timeline is read off the logged-in desktop homepage the way a
browser sees it and the charts off the mobile JSON API, both with your existing
browser session, so there is no app to register and no API key to apply for.

```
douban-tui   unread · 21                       updated 14:32:07

  阿北        说 今天天气不错，出门走走                                  2h
▌ 某友邻      看过 沙丘2 ↻ 影评人: 视觉盛宴…                             3h

      某友邻 看过

      视觉盛宴，但节奏比第一部松弛。

      https://www.douban.com/people/xxx/status/1234567890/
  …
```

## Requirements

- Go 1.26+ (to build from source)
- For login only: a Chromium-family browser (Brave, Chrome, Chromium, Edge, …).

## Quick start

This app ships as part of [`tui`](../../README.md), so the usual way in is
`tui douban`. To run it on its own from a source checkout:

```sh
make auth     # opens a browser; log into douban; saves the session to ~/.config/tui/env
make run      # build and launch the TUI
```

`make check` fetches one page of the following timeline and prints the result,
handy for confirming the session works before opening the UI.

## Keys

| Key        | Action                          |
| ---------- | ------------------------------- |
| `j` / `k`  | move down / up (marks the status you leave read; scrolls a long expanded status line by line) |
| `g` / `G`  | jump to top / bottom            |
| `space`    | expand / collapse the status (expanding marks it read) |
| `r`        | mark the status read            |
| `K`        | keep the status unread (scrolling won't mark it; `K` again unlocks) |
| `u`        | toggle unread-only vs show-all (read statuses greyed) |
| `tab`      | show the left author column (hidden by default) |
| `o`        | render the status in [carbonyl](https://github.com/genkio/carbonyl) in the terminal; `q` quits back |
| `O`        | same as `o` but with `--graphics` (kitty graphics protocol) |
| `b`        | open in the browser            |
| `R`        | refresh the timeline           |
| `?`        | toggle help                     |
| `q` / esc  | collapse an expanded status, else quit |

## Configuration

Secrets live only in the environment (never in a config file). Everything else
can go in `~/.config/douban-tui/config.toml` or be set via these environment
variables (shell exports win over `config.toml`):

| Variable              | Default    | Meaning                                    |
| --------------------- | ---------- | ------------------------------------------ |
| `DBTUI_COOKIE`        | (required) | the full `douban.com` browser cookie set   |
| `DBTUI_MAX_STATUSES`  | `50`       | statuses to fetch per refresh              |
| `DBTUI_UNREAD_ONLY`   | `true`     | hide read statuses on refresh; `false` keeps them greyed |
| `DBTUI_THEME`         | `auto`     | `auto` (match terminal), `light`, `dark`   |
| `DBTUI_REFRESH`       | off        | auto-refresh interval, e.g. `2m`; keep it slow |
| `DBTUI_CHARTS`        | the three below | comma-separated 榜单 to mix in; set it empty for none |
| `DBTUI_UA`            | (built-in) | User-Agent override, to match the cookie's browser |

See `.env.sample` for a copy-paste template.

## Charts

Douban's weekly 榜单 ride in the same feed as the timeline, one entry per
ranked title, dated by the list's own update time so they land where they were
published. Out of the box:

| Chart                     | 榜单           |
| ------------------------- | -------------- |
| `movie_weekly_best`       | 一周口碑电影榜 |
| `tv_global_best_weekly`   | 全球口碑剧集榜 |
| `show_global_best_weekly` | 国外口碑综艺榜 |

Any subject collection works — the id is the tail of its address, so
`m.douban.com/subject_collection/book_weekly_best` is `book_weekly_best`:

```toml
charts = ["movie_weekly_best", "book_weekly_best"]   # or [] for none
```

A title read once stays read, so after the first load only titles new to a
chart come back unread. The lists turn over weekly, so fetched chart responses
are cached for six hours in `feed.db`.

## Authentication

`make auth` (or `tui douban --auth`) opens a Chromium-family browser on
douban's login page, waits for you to finish logging in (QR code, SMS, or
password — whatever douban offers), then captures the `douban.com` cookie set
(the `dbcl2` login cookie is the important one) into `~/.config/tui/env`.

When the session eventually expires, the app reports it as stale and the
launcher shows a red dot; re-run the same command to refresh it.

## Read tracking

Douban exposes no read state for the timeline, so douban-tui keeps status ids in
`~/.local/state/tui/feed.db` without an application-level cap. The
launcher's merged "all" view flushes into the same store, so read state stays
consistent everywhere.
