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
tokens that project needs.

See each app's README for its keys, configuration, and cookie-capture details.
