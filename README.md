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
| [`bilibili`](plugins/bilibili/README.md)   | bilibili 动态: video posts of who you follow  |

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
  state/<app>-tui/read.json    read state (x, reddit, douban, bilibili)
  state/tui/saved.json         the web view's saved list
  state/tui/feed.json          the web view's backlog cache and read marks
  state/tui/keywords.json      the web view's block list
  state/tui/blocked.json       what those keywords kept out of the feed
  config/<app>-tui/config.toml per-app settings
```

Migrate existing state once:

```sh
mkdir -p "$TUI_STATE_DIR"
mv ~/.config/tui/env "$TUI_STATE_DIR/env"
for d in ~/.local/state/*-tui; do mkdir -p "$TUI_STATE_DIR/state/$(basename $d)" && mv $d/* "$TUI_STATE_DIR/state/$(basename $d)/"; done
```

The flag works on the launcher and on a single app alike (`tui --state-dir
<dir>`, `tui x --state-dir <dir>`), and an app the picker opens inherits
whatever the launcher was given. The browser login profile
(`~/.config/tui/profile`) deliberately stays local: live Chromium profiles
don't survive file syncing, and the captured session values in `env` are what
the apps actually use. Read marks are whole-file JSON saves, so triage from one
device at a time; concurrent edits end as Dropbox conflict copies. The same goes
double for `state/tui/feed.json`: run one `tui --web`, since it's the only
record of a backlog Inoreader has been told you've read.

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

One exception: once `tui --web` has drained Inoreader (see below), Inoreader's
own count is zero, so the picker reads that badge off the web server's backlog
instead and labels it **`N unread on --web`** — those items are triaged there,
and opening the Inoreader TUI won't show them.

```sh
make build    # build the launcher + all four TUIs
make x        # build just one (also: make inoreader, make slack)
make clean
```

## Web view (mobile)

The same `all` timeline is available as a mobile-friendly web page, so you can
triage from your phone or any other device on your Tailscale network:

```sh
tui --web                    # serve the all timeline on 0.0.0.0:8080
tui --web --web-addr :9000   # custom port
tui --web --swipe            # one card at a time, swiped through (see below)
tui --web --web-fetch 30m    # fetch less often (default 10m; 0 = on demand only)
tui --web --web-drain=false  # leave Inoreader's own unread list alone (see below)
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
to copying it where the browser has no share API). Attached media plays on the
page: a video (or a linked YouTube clip) as a click-to-play player, a podcast
episode from a reader as an audio bar. A reddit post whose video lives on
**redgifs** is only a link, so its footer gets a **video** button instead: tap
it and the server resolves the clip and plays it on the card, tap again to put
it away — it is fetched once, so bringing it back costs nothing. A **bilibili**
post is the same problem one step further on: its mp4 hides behind a play-URL
call and its CDN refuses anyone without a `bilibili.com` Referer, which a page
cannot forge — so the card's player points at the server's own `/bili` route,
which resolves the video, streams it with the headers bilibili wants, and passes
`Range` through so seeking and rotate-to-fullscreen work as they do for any
other clip. **keep** asks that route for the same bytes as a download. Playback is
set once for the whole feed from any card's footer — **2×** by default, tapped
down to 1× — and video also starts muted, where an episode never does. The
badge in the player's corner states how long the clip runs before a tap
downloads any of it, then counts down what's left of it once it's playing. A
**loop** button at the end of the row repeats video (off by default). A footer
with a player in it outgrows a phone, so the row scrolls sideways. Since
scroll-to-read can't reach the last few cards, a **mark all read** button sits
at the end of the feed to clear the tail in one tap. x's **For You** is a chip of
its own in the header (see below) rather than something offered at the end of the
feed. It reuses the same `--json` / `--mark-read`
contract the terminal `all` view uses, so read state stays consistent between
the TUI and the page — mark something read and it's read in the app too (see
**The backlog cache** below for the one place that changes). Items are sorted
**oldest-first** (triage in the order they arrived;
`?order=desc` flips it), and the saved list is ordered by **when you saved
things, newest first**. The page uses the Inter webfont (with system
fallbacks) for a polished read.

Above the first card is one wrapping row of **chips**: a group for the
**source** (`𝕏`, `rdt`, `ino`, …) followed by one for what the item carries
(**text**, **video**, **audio**), each chip counting the unread items it would
bring — so reading takes the chip numbers down alongside the header's. One is on
at a time, and tapping it loads a page of that chip's items and nothing else
(`?app=reddit`, `?type=video`), so the counts always say what a pick would bring
rather than what is left of it. The header's own count stays every source's
unread whichever chip is on — the chips already say what each one holds, and
repeating the picked one there would leave nothing stating the whole. Tapping the
chip that is on, or **clear**, brings the whole list back. Any marks that haven't
reached the server yet are flushed before the page goes.

Next to x's own blue chip sits a second, **black** one: the same `𝕏`, no label.
That is x's other timeline, and it is the one pick that fetches rather than
reads. From anywhere else it is the icon alone — none of that timeline is kept,
so there is nothing to count; on the page the fetch serves, it states the round
it just brought, next to the icon like every other chip's number, and takes it
down as you read (the header's backlog count stays put there: a round is no part
of it). Green when the fetch landed, red when it didn't. Tapping it
again is another round: it's an endless firehose, not a list you get to the end
of, which is why nothing from it enters the backlog.

On the feed a source chip is also that service's **status light**: its count is
green when the last sweep of it worked and red when it didn't, so a stale number
looks stale. Every logged-in app gets a chip whether or not it has anything on
the page — a service that failed to fetch has nothing to show and is exactly the
one worth seeing — and anything still cached from an app you've since logged out
of keeps a chip too, just without a colour. On the saved list, read off disk with
no service to be up or down, the chips are plain: there, a group that would light
up the whole list narrows nothing and isn't drawn, so a saved list of one kind
from one app gets no chips at all.

The chips are also what **mark all read** applies to: with one on, the page is
that chip's items alone, so it clears those and leaves the rest unread — you can
sweep reddit away and keep the articles for later. The pick lives in the URL, so
the reload that fetches the next window of a deep backlog comes back to it.

A saved item also remembers **where you left off**. The position in its player
is posted back as you watch or listen and rides along in `saved.json`, so
reopening the list picks up there — on the same phone, or on another device
reading the same synced store. It covers video, podcast audio, redgifs clips
and YouTube embeds alike, and pins the position to the stream it belongs to, so
a card carrying both a clip and an episode can't resume the wrong one. Getting
within ten seconds of the end counts as finished rather than paused, so the
next visit starts it over instead of dropping you at the credits. Feed cards
resume too when the item is already saved.

The page is server-rendered and responsive (cards stack full-width, tap-sized
targets, follows your phone's light/dark theme). A feed page comes off the
cache and is instant, but the For You chip scrapes x live, so a tap that
refetches puts a **loading…** cover over the page you tapped from rather
than leaving it looking idle. `?json=1` returns the whole backlog as JSON for
scripts (no window). Runs indefinitely until you Ctrl-C.

### The backlog cache

A page load used to scrape all six services before it sent a byte, which is why
refreshing took the best part of a minute, and why the unread count was never
the real one: each app hands over its newest page (40-50 items) and no more.

Now a background sweeper does the fetching, every **10 minutes** by default and
jittered ±15% so the requests don't arrive on a machine-perfect cadence (the
services within one sweep are spread out too). What it finds accumulates in
`state/tui/feed.json`, keyed by app and id, and a page load just reads that
file. Two things follow: the page is as fast as the saved list already was, and
the count is the **real** backlog, because it grows across sweeps instead of
being replaced by one. Tap the count to ask for a fetch now; it says
`fetching…` while one is in flight and how stale it is otherwise (`4m`).

A page carries at most **500 cards** — a phone rendering a thousand bodies,
posters and players would crawl — while the header counts the whole thing. The
button says *"mark these 500 read"* when there's more behind it, and clearing
them reloads into the next batch, so the number stays honest and the triage loop
keeps going.

**Read marks are ours now.** Marking something read writes to the cache and the
request returns; carrying it to the app's own `--mark-read` is a background job,
retried until it lands, across a restart included. That's what makes marking a
few hundred items at once work: Inoreader spends an HTTP round trip per article,
so a page-sized batch could never answer inside one request. The mark still
reaches the app, so the TUI agrees about it as before.

If a mark can't reach the server at all — the laptop went to sleep, the Wi-Fi
dropped, the server was restarted mid-swipe — the page **repairs itself** rather
than swallowing it. Those greyed cards and lowered counts are a state the server
doesn't have, and swiping on only widens the gap. So: one retry a second and a
half later, which is all a dropped packet or a waking phone needs and costs you
nothing; if that fails too, the page reloads. What comes back is what the server
actually has, so reads that never landed show up unread again — a couple of
cards to redo, in exchange for a page you can trust. The fresh page says once,
in passing, why it reloaded.

**Inoreader is drained.** It's the one service that can't page at all: its
"load more" call returns a stale copy of the first page, so the only way to
reach article 51 is to tell it the first fifty are read and ask again. The
sweeper does exactly that, up to 8 rounds a sweep, and the items land in the
cache **unread for you** — read upstream is not read by you. Persisting comes
first and marking second, always: an article Inoreader thinks you've read but
that never reached the cache would be gone for good.

The consequences are worth knowing:

- inoreader.com will show **0 unread** from then on. Your backlog lives in
  `feed.json`, and `--web` is where you read it.
- the standalone `tui inoreader` TUI and the terminal `all` view fetch live, so
  they'll look empty. The picker's badge reads the cache instead and says
  `unread on --web` so it isn't lying to you.
- `--web-drain=false` turns it off, at the cost of only ever seeing Inoreader's
  first page. Every other service pages honestly (folo walks a cursor) or has no
  server-side read state at all (x, reddit, douban, bilibili keep a local
  `read.json`), and none of them is ever marked read at fetch time.

Two housekeeping notes. The cache is a **single-writer** file: run one
`tui --web`, since it rewrites the whole thing each sweep and a Dropbox conflict
copy here is lost backlog rather than an annoyance. And it's bounded — read
entries are pruned after two weeks, and past 6000 entries the oldest read ones
go first, unread ones only as a last resort, with a line on stderr when that
happens.

### Blocking by keyword

Some things you don't want to triage, you want to never see. The header's
**N blocked** link opens a list of exactly that, and the **N keywords** link on
it opens the browser's own modal with the list of words in a textarea, one per
line. Edit, save, done — there is no per-row add and delete, because the list
already is a list.

A sweep screens what it fetched before the cache sees any of it: a post whose
**title** carries one of the words is kept out of `feed.json` and filed in
`blocked.json` instead, so it never becomes backlog you have to clear. Matching
is plain case-insensitive substring anywhere in the title, which is what
"keep posts about X out" means for a Chinese title as much as an English one.
Only the title is read — a post that carries its whole text as its title (an x
post has no headline of its own) is never blocked, and matching a body would
block a great deal more than you asked for.

Saving the list also re-screens the backlog you already have, since a word is
added because of something on the screen right now. Nothing upstream is told
anything: the post stays unread there, and it is simply fetched and filed again
each sweep, deduplicated by app and id.

The blocked list renders as **titles alone** — no body, no player, no stills —
with the keyword that caught each one on its row, so a long list stays
scannable and it's obvious which word is doing too much work. The chips narrow
it the way they narrow the saved list, and nothing on it can be marked read,
because none of it was ever unread. `blocked.json` is a plain record you can
delete whenever you've seen enough of it; it holds 2000 posts, oldest first out.

### Swipe mode

`tui --web --swipe` serves the same feed as a deck instead of a scroll: one
card at a time, centered on the screen, with the rest stacked behind it.

**Swipe left** to mark the card read and deal the next one; **swipe right** to
walk back through what you've already dealt, one card per pull, as far back as
the first one (they stay read — it's a second look, not an undo). **→**/**l**
deals the next one and **←**/**h** walks back, and a mouse can drag. Only
sideways is a
gesture: up and down scroll the page as usual, and everything else is a footer
button, saving included. Once the deck is dealt out the two floating ticks go
away and a note says so; where to go next is the chip row, which the deck
carries above it like the feed does.

Cards get a longer text budget here (one card owns the screen) but stay clipped
to roughly a screenful, so the footer actions — open, save, share, image
toggle, video controls, the video **keep** download — are always in reach
(swipe the row itself sideways when they outrun the screen; only the card
behind it throws).
Anything past the clip sits behind the same "+N words" toggle.

The two gestures also have buttons, floating in the bottom corners under either
thumb: a **tick** on the left marks the card on screen read and deals the next,
the same as a left swipe, and a **double tick** on the right clears the whole
deck. Marks rather than words, because the gestures they stand in for have no
words either and a label long enough to explain itself would reach halfway
across the card; a long press says which is which. Both are drawn as SVG rather
than set in ✓ characters, so the pair shares one stroke weight and one scale.
The saved list is for re-reading rather than triage, so it stays a scrolling
list even in swipe mode.

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
