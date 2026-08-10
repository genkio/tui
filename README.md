# tui

A small launcher for my terminal apps, each a cookie-stealth TUI over a site I
read daily:

| App                      | What it is                                  |
| ------------------------ | ------------------------------------------- |
| **all**                  | Every app's unread items in one merged, time-sorted feed |
| [`x`](plugins/x/README.md)               | x.com home timelines (For You / Following)  |
| [`inoreader`](plugins/inoreader/README.md) | Inoreader unread article triage             |
| [`slack`](plugins/slack/README.md)         | Slack unread messages and threads           |
| [`folo`](plugins/folo/README.md)           | Folo pending (unread) article triage        |
| [`reddit`](plugins/reddit/README.md)       | Reddit home timeline                         |
| [`douban`](plugins/douban/README.md)       | Douban following timeline (友邻广播)          |

The whole family ships as one binary; each app is a plugin under `plugins/`.

## Install

```sh
brew install genkio/tap/tui
tui                    # open the picker
```

Log into an app with `tui <app> --auth` (e.g. `tui x --auth`): it opens a
Chromium-family browser (Brave, Chrome, Chromium, Edge, …) to capture your
session, so install one if you haven't. Credentials and settings live in
`~/.config/tui/env`. Reading a story with `o` uses
[carbonyl](https://github.com/genkio/carbonyl), installed as a dependency.

### Syncing state between devices

Point `TUI_STATE_DIR` (or `tui --state-dir <dir>`) at a synced folder and all
persisted state moves under it, so sessions and read marks follow you across
machines:

```sh
export TUI_STATE_DIR=~/Dropbox/tui   # in your shell profile; or: tui --state-dir ~/Dropbox/tui
```

```
$TUI_STATE_DIR/
  env                          credentials for every app (chmod 600)
  state/<app>-tui/read.json    read state (x, reddit, douban)
  config/<app>-tui/config.toml per-app settings
```

Migrate existing state once:

```sh
mkdir -p "$TUI_STATE_DIR"
mv ~/.config/tui/env "$TUI_STATE_DIR/env"
for d in ~/.local/state/*-tui; do mkdir -p "$TUI_STATE_DIR/state/$(basename $d)" && mv $d/* "$TUI_STATE_DIR/state/$(basename $d)/"; done
```

The flag only affects the launcher; when running a plugin directly
(`tui x`), set the env var. The browser login profile
(`~/.config/tui/profile`) deliberately stays local: live Chromium profiles
don't survive file syncing, and the captured session values in `env` are what
the apps actually use. Read marks are whole-file JSON saves, so triage from one
device at a time; concurrent edits end as Dropbox conflict copies.

## Use

```sh
tui                    # installed via Homebrew
make run               # from a source checkout (builds ./tui)
```

Pick an app and press enter. If it's already logged in it opens straight away;
if not, the launcher runs its `--auth` browser login first, then opens it.
Inside an app, `q` drops back to the picker; `q` again quits.

## The `all` timeline

The first pick is **all**: one feed of the unread items from every logged-in
reader app (`x`, `inoreader`, `folo`; slack is a chat model, not a stream),
merged and sorted newest-first, each row tagged with a colored source chip
(`𝕏`, `ino`, `folo`, `rdt`). It's the whole morning's backlog in one place.

Triage works exactly like the individual apps: `j`/`k` move (and mark the row
you leave read), `space` expands the body inline, `o`/`O` read it in carbonyl,
`b` opens the browser, `c` copies the URL, `tab` shows the source names
(`@handle` / feed title, hidden by default so rows are all content), `R`
refetches, `q` backs out to the picker. Marking a row read flushes to that app's own read state (x's local
store, or Inoreader/Folo's server), so it's read everywhere, and the picker's
counts update the moment you return.

Once you've read everything, `all` offers one more thing: if x is logged in and
you were on its **Following** feed, it shows *"All read — press `f` to continue
on x For You"*. `f` swaps the x source to the **For You** timeline and refetches
(backing out of `all` and re-entering returns to Following, and a subsequent
refresh keeps whatever tab is live).

Sorting is by publish time: x and Folo carry an exact timestamp; Inoreader
exposes only a relative age (`2h`), so its items are placed from that. An item
with no resolvable time sinks to the bottom rather than jumping the queue.

Which apps qualify is a single `feed` flag in the launcher's app registry, so a
new reader app joins the merged feed as soon as it implements the `make json` /
`make mark-read` contract and flips that flag on.

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

## Web view (mobile)

The same `all` timeline is available as a mobile-friendly web page, so you can
triage from your phone or any other device on your Tailscale network:

```sh
tui --web                 # serve the all timeline on 0.0.0.0:8080
tui --web --web-addr :9000   # custom port
tui --web --swipe         # one card at a time, swiped through (see below)
```

The server binds `0.0.0.0` by default so your other devices reach it, and
prints the Tailscale URL to open when `tailscale` is on PATH. It exposes only
the merged `all` view (no per-app views, no login flow): each logged-in feed
app's unread items, newest-first. Reading happens by scrolling, like the
terminal: a card is marked read the moment it scrolls fully off the top of the
screen and is muted (greyed) in place — it stays in the list so you can see
what you've read. Long posts are clipped, and the ellipsis is followed by a
**"+N words"** count that expands the rest when tapped (a **less** button in
the footer collapses it again). Each card's footer has a **link icon** that
opens the original post in a new tab (the only way to leave the page) and a
**share** button that hands the post to your phone's share sheet (falling back
to copying it where the browser has no share API). Since scroll-to-read can't
reach the last few cards, a **mark all read** button sits at the end of the
feed to clear the tail in one tap. Once everything is read, if x is logged in,
a link at the bottom offers to **continue on x For You**; following it switches
x to For You (ephemerally — reloading the page returns to the Following
default). The offer stands on For You too, worded as **another round**, since
each visit refetches the timeline — so an emptied list is never a dead end. It
reuses the same `--json` / `--mark-read`
contract the terminal `all` view uses, so read state stays consistent between
the TUI and the page — mark something read and it's read in the app, and vice
versa. Items are sorted **oldest-first** (triage in the order they arrived;
`?order=desc` flips it), and the saved list is ordered by **when you saved
things, newest first**. The page uses the Inter webfont (with system
fallbacks) for a polished read.

The page is server-rendered and responsive (cards stack full-width, tap-sized
targets, follows your phone's light/dark theme). A feed page is scraped before
a byte of it is sent, so following a link that refetches puts a **loading…**
cover over the page you tapped from rather than leaving it looking idle.
`?json=1` returns the same feed as JSON for scripts. Runs indefinitely until
you Ctrl-C.

### Swipe mode

`tui --web --swipe` serves the same feed as a deck instead of a scroll: one
card at a time, centered on the screen, with the rest stacked behind it.

**Swipe left** to mark the card read and deal the next one; **swipe right** to
walk back through what you've already dealt, one card per pull, as far back as
the first one (they stay read — it's a second look, not an undo). **→** and
**←** do the same with a keyboard, and a mouse can drag. Only sideways is a
gesture: up and down scroll the page as usual, and everything else is a footer
button, saving included. Once the deck is dealt out the floating **mark all
read** goes away and, if x is logged in, the end note offers **For You** as
somewhere to go next — another round of it if that is where you already were.

Cards get a longer text budget here (one card owns the screen) but stay clipped
to roughly a screenful, so the footer actions — open, save, share, image
toggle, video controls, the video **keep** download — are always in reach.
Anything past the clip sits behind the same "+N words" toggle. **Mark all
read** floats as a pill in the bottom-right corner. The saved list is for
re-reading rather than triage, so it stays a scrolling list even in swipe
mode.

### macOS firewall vs. source builds

If the macOS firewall is on, other devices can't reach the page even though it
loads fine on localhost: the firewall judges the **listening app** (not
tailscaled), and it silently blocks **unsigned** binaries — on Sequoia without
the old "allow incoming connections?" popup, and allow rules don't stick until
the binary is signed. Go builds are unsigned on Intel Macs (Apple Silicon
ad-hoc signs automatically), so a source-built `./tui` is exactly that.

`make launcher` / `make build` re-sign the binary automatically after every
build (rebuilding wipes the signature). But the firewall remembers your
"Allow" by code signature, and an **ad-hoc** signature (`codesign -s -`) is a
new identity on every build — so each rebuild re-triggers the "accept incoming
network connections?" popup. To answer it once and for all, create a stable
self-signed signing cert (one time, in a regular terminal — keychain prompts
can't appear in non-interactive sessions):

```sh
make signing-cert   # creates "tui-codesign" in the login keychain; approve the trust dialog
make                # first signing use: keychain dialog → Always Allow
make firewall       # register the signed binary with the firewall (asks for sudo)
```

then restart `tui --web` and Allow the popup one last time. From then on every
build signs with the same identity, so the firewall rule keeps matching across
rebuilds. `make launcher` uses the cert automatically whenever it exists and
falls back to ad-hoc signing otherwise; if serving `--web` from another Mac,
run `make signing-cert` there once too. If the CLI route fails, the GUI
equivalent is Keychain Access → Certificate Assistant → Create a Certificate
(name `tui-codesign`, Self-Signed Root, Code Signing), then Trust → Code
Signing: Always Trust.

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

The `all` timeline extends that same contract: each feed app also answers `make
json` (its unread items as a normalized JSON array) and `make mark-read` (ids on
stdin), so the launcher can render and triage across apps without importing any
of them. The launcher owns only the merge, sort, and UI; each app still owns its
own session and read state.

See each app's README for its keys, configuration, and cookie-capture details.
