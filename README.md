# tui

A small launcher for my terminal apps, each a cookie-stealth TUI over a site I
read daily:

| App                      | What it is                                  |
| ------------------------ | ------------------------------------------- |
| [`x`](x/README.md)               | x.com home timelines (For You / Following)  |
| [`inoreader`](inoreader/README.md) | Inoreader unread article triage             |
| [`slack`](slack/README.md)         | Slack unread messages and threads           |
| [`folo`](folo/README.md)           | Folo pending (unread) article triage        |

## Use

```sh
make run      # build the launcher and open the picker
```

Pick an app and press enter. If it's already logged in it opens straight away;
if not, the launcher runs that project's `make auth` first (browser login), then
opens it. Inside an app, `q` drops back to the picker; `q` again quits.

For every logged-in app the picker shows an unread count next to it, refreshed
every 5 minutes (and again the moment you return from an app, since you've
likely just read something). The header shows how long ago the counts were last
fetched (`updated 2m ago`). Polling pauses while you're inside a TUI so the
launcher isn't hitting the same service the app already is. Press `r` to refresh
now.

```sh
make run                 # default 5-minute poll
TUI_POLL=2m make run     # custom interval (env)
./tui --poll 0           # disable polling; press r to count on demand
```

Counts are one cheap fetch per service (the newest page), shown as `N` or `N+`
when the count hits the fetch cap and there's likely more. The x count reuses
its local read-tracking store, so it means "unread in your latest posts".

A service showing a capped `N+` is **skipped by the periodic poll**: re-fetching
can't move the badge off the ceiling, so it's wasted requests. It's re-checked
only when you return from that app (you may have read it down) or press `r`.
Services below the cap keep polling, so new items still bump the number.

```sh
make build    # build the launcher + all four TUIs
make x        # build just one (also: make inoreader, make slack)
make clean
```

## Layout

Each app stays a self-contained Go module with its own `Makefile`, `README`,
and `.env`, so it still builds and runs on its own:

```sh
cd x && make run      # same for inoreader, slack, folo
```

The launcher (`launcher/`) just runs the selected project's `make run` / `make
auth` as a subprocess, which is why quitting a child returns to the picker. It
decides "logged in?" by sourcing each project's `.env` and checking for the
tokens that project needs. Unread counts work the same way: it runs each
project's `make count`, which prints a single number the picker shows as a
badge.

See each app's README for its keys, configuration, and cookie-capture details.
