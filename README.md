# tui

A feed service with terminal and web clients, plus standalone cookie-stealth
TUIs for the sites I read daily:

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
tui serve              # start the feed service
tui                    # open its terminal client
```

Log into an app with `tui <app> --auth` (e.g. `tui x --auth`): it opens a
Chromium-family browser (Brave, Chrome, Chromium, Edge, …) to capture your
session, so install one if you haven't. Credentials and settings live in
`~/.config/tui/env`. Reading a story with `o` uses
[carbonyl](https://github.com/genkio/carbonyl), installed as a dependency.

### Syncing state between devices

Point `TUI_SYNC_DIR` (or `tui serve --sync-dir <dir>`) at a synced folder to carry
sessions, settings, read marks, and a snapshot of the feed database across
machines:

```sh
export TUI_SYNC_DIR=~/Dropbox/tui
tui serve                           # or: tui serve --sync-dir ~/Dropbox/tui
```

```
$TUI_SYNC_DIR/
  env                          credentials for every app (chmod 600)
  feed.db                      snapshot of the feed database
  config/<app>-tui/config.toml per-app settings
```

Migrate existing state once:

```sh
mkdir -p "$TUI_SYNC_DIR"
mv ~/.config/tui/env "$TUI_SYNC_DIR/env"
for d in ~/.local/state/*-tui; do mkdir -p "$TUI_SYNC_DIR/state/$(basename $d)" && mv $d/* "$TUI_SYNC_DIR/state/$(basename $d)/"; done
```

The flag also works on a standalone app (`tui x --sync-dir <dir>`). Terminal
and web feed clients do not need it because they never open the database or
read credentials. The browser login profile
(`~/.config/tui/profile`) deliberately stays local: live Chromium profiles
don't survive file syncing, and the captured session values in `env` are what
the apps actually use. Feed content, plugin read marks, saved and blocked
items, keywords, and the Douban chart cache all live in the local database.
The server snapshots it into the sync directory after each fetch.

## Use

```sh
tui serve --sync-dir ~/Dropbox/tui  # database owner and background fetcher
tui                                   # terminal All client on localhost
tui web                               # open the web client on localhost
```

The clients default to `http://127.0.0.1:8080`. Point either one at another
machine with `--server URL`, or set `TUI_SERVER_URL`. Standalone apps remain
available by name: `tui x`, `tui reddit`, and so on.

## The `all` timeline

Bare `tui` opens **all**: one feed of the unread items from every logged-in
reader app (`x`, `inoreader`, `folo`; slack is a chat model, not a stream),
merged and sorted newest-first, each row tagged with a colored source chip
(`𝕏`, `ino`, `folo`, `rdt`). It's the whole morning's backlog in one place.

Triage works exactly like the individual apps: `j`/`k` move (and mark the row
you leave read), `space` expands the body inline, `o`/`O` read it in carbonyl,
`b` opens the browser, `c` copies the URL, `s` saves it, and `tab` shows the source names
(`@handle` / feed title, hidden by default so rows are all content), `R`
reloads the service cache, and `q` quits. Marking a row read posts it to the
service, which records it in `feed.db` and flushes it to the source in the
background. The web and terminal clients therefore read the same backlog.
Video titles start with `🎬`; audio titles start with `🔊`. Text items have no
prefix.

Sorting is by publish time: x and Folo carry an exact timestamp; Inoreader
exposes only a relative age (`2h`), so its items are placed from that. An item
with no resolvable time sinks to the bottom rather than jumping the queue.

Which apps qualify is a single `feed` flag in the app registry, so a
new reader app joins the merged feed as soon as it implements the `make json` /
`make mark-read` contract and flips that flag on.

```sh
make build    # build tui and every standalone app binary
make x        # build just one (also: make inoreader, make slack)
make clean
```

## Feed service and web view

The same `all` timeline is available as a mobile-friendly web page, so you can
triage from your phone or any other device on your Tailscale network:

```sh
tui serve --sync-dir ~/Dropbox/tui
tui serve --addr :9000 --fetch-every 30m
tui serve --drain-inoreader=false
tui web
tui web --server http://100.121.244.89:8080
```

`tui serve` listens on `0.0.0.0:8080` by default, fetches immediately, and then
fetches every ten minutes with jitter. It serves the API and HTML application
from the same address. `tui web` only opens that address in the local browser;
it does not start another process.

The live database stays at `~/.local/state/tui/feed.db` (or
`$XDG_STATE_HOME/tui/feed.db`). When a sync directory is set, every completed
fetch writes a consistent SQLite snapshot to `$TUI_SYNC_DIR/feed.db`, then
atomically replaces the old snapshot.
If the local database is missing, startup restores that snapshot. Without a
sync directory, the database remains local and no snapshot is made.

The first startup imports the old `feed.json`, `saved.json`, `keywords.json`,
`blocked.json`, each plugin's `read.json`, and Douban's `charts.json`. It leaves
them alone during import. Once the server starts and the synced `feed.db`
snapshot exists, those legacy JSON state files can be deleted.

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
to copying it where the browser has no share API), and a **link** that opens
that one item on a page of its own (see **One item, one URL**). Attached media plays on the
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
at the end of the feed to clear the full backlog in one tap. x's **For You** is a
chip of its own in the header, in black beside x's blue one: it is a source like
any other (see **x has two of them** below). Read state stays consistent between
the TUI and the page — mark something read and it's read in the app too (see
**The backlog cache** below for the one place that changes). Items are sorted
**oldest-first** (triage in the order they arrived), and the saved list is
ordered by **when you saved things, newest first**. At the far end of the
header is the order the feed is in — **↑ oldest** or **↓ newest** — and tapping
it turns the whole backlog around, whichever chip is on (`?order=desc` is the
same thing by hand). The order is the server's to do, since a page carries only
a window of the backlog, but the browser remembers which one you asked for and
asks for it again on the next visit. Beside it is the shape the feed is in —
**▤ list** or **▭ deck** — which works the same way (see **Swipe mode**). The
page uses the Inter webfont (with system fallbacks) for a polished read.

Where the page starts is the page's business, not the browser's: a card is
marked read by scrolling past it, so a reload that came back at the old offset
(Firefox does this) would report every card above it as read without you having
seen any of them. So the feed asks for no scroll restoration and starts at the
top of what's left, and any card that is somehow already above the viewport is
exempt until it has actually been on screen. Reads are batched for a moment
before they're sent, and leaving inside that window — a refresh, a closed tab —
sends them with a beacon rather than losing them.

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

A source chip with anything unread behind it is drawn as a **pair**: the label
picks that source, and the ✦ beside it asks for a briefing of everything the
chip counts (see **Summarizing a backlog**). A chip at zero is only a chip —
there is nothing there to brief anybody on.

Next to x's own blue chip sits a second, **black** one: the same `𝕏`, no label.
That is x's other timeline, and it is the one pick that fetches rather than
reads. From anywhere else it is the icon alone — none of that timeline is kept,
so there is nothing to count; on the page the fetch serves, it states the round
it just brought, next to the icon like every other chip's number, and takes it
down as you read (the header's backlog count stays put there: a round is no part
of it). Green when the fetch landed, red when it didn't. Tapping it
again is another round: it's an endless firehose, not a list you get to the end
of, which is why nothing from it enters the backlog.

Two sources arrive already sorted into streams, and picking one of them opens a
**second row** underneath: reddit's subreddits, Inoreader's feeds, each with its
own count (`?app=reddit&sub=r%2Fgolang`). It stacks on the source pick rather
than replacing it, so the row is a way into one subreddit without leaving
reddit, and tapping the one that is on hands the whole source back. They are
sorted busiest first and trimmed to a **single line** — a source can carry
dozens, and a block of chips that pushes the first card off the screen is worse
than not offering the narrowing at all — with the rest behind **+N more**, which
says **less** once it's open. Reading a card there takes the stream's chip, the
source chip and the header's count down together.

On the feed a source chip is also that service's **status light**: its count is
green when the last sweep of it worked and red when it didn't, so a stale number
looks stale. Every logged-in app gets a chip whether or not it has anything on
the page — a service that failed to fetch has nothing to show and is exactly the
one worth seeing — and anything still cached from an app you've since logged out
of keeps a chip too, just without a colour. On the saved list, read off disk with
no service to be up or down, the chips are plain: there, a group that would light
up the whole list narrows nothing and isn't drawn, so a saved list of one kind
from one app gets no chips at all.

The saved header's **full** / **compact** control switches between complete
cards and title-only rows. The mode rides in the URL, so source and type chips
keep it while narrowing the saved list; compact rows still link to the original
item but leave bodies, media, controls, and tags out.

The chips are also what **mark all read** applies to: with one on, the page is
that chip's items alone, so it clears those and leaves the rest unread — you can
sweep reddit away and keep the articles for later. The server applies that pick
to the complete SQLite backlog, not only the 100 list cards or 20 deck cards sent
to the browser.

A saved item also remembers **where you left off**. The position in its player
is posted back as you watch or listen and rides along in `feed.db`, so
reopening the list picks up there — on the same phone, or on another device
reading the same synced store. It covers video, podcast audio, redgifs clips
and YouTube embeds alike, and pins the position to the stream it belongs to, so
a card carrying both a clip and an episode can't resume the wrong one. Getting
within ten seconds of the end counts as finished rather than paused, so the
next visit starts it over instead of dropping you at the credits. Feed cards
resume too when the item is already saved.

The page is server-rendered and responsive (cards stack full-width, tap-sized
targets, follows your phone's light/dark theme). A feed page comes off the
cache and is instant to fetch, but a chip over a few hundred cards still has to
render them, so a tap puts a **loading…** cover over the page you tapped from
rather than leaving it looking idle. `?json=1` returns the whole backlog as JSON for
scripts (no window). Runs indefinitely until you Ctrl-C.

### The backlog cache

A page load used to scrape all six services before it sent a byte, which is why
refreshing took the best part of a minute, and why the unread count was never
the real one: each app hands over its newest page (40-50 items) and no more.

Now a background sweeper does the fetching, every **10 minutes** by default and
jittered ±15% so the requests don't arrive on a machine-perfect cadence (the
services within one sweep are spread out too). What it finds accumulates in
the `feed_items` table in the local `feed.db`, keyed by app and id, and a page
load reads that database. Two things follow: the page is as fast as the saved
list already was, and
the count is the **real** backlog, because it grows across sweeps instead of
being replaced by one. The count says `fetching…` while a sweep is in flight
and how stale the last one is otherwise (`4m`); tapping it opens settings.

The scrolling list carries at most **100 cards** and the swipe deck carries
**20**. The header and chips still count the complete backlog. Finishing a
partial deck flushes its read marks and loads the next 20. Mark-all applies to
the complete filtered backlog rather than either client window.

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
  `feed.db`, and the terminal and web clients both read it through `tui serve`.
- the standalone `tui inoreader` TUI fetches live, so it will look empty. Bare
  `tui` reads the service backlog instead.
- `tui serve --drain-inoreader=false` turns draining off, at the cost of only ever seeing Inoreader's
  first page. Every other service pages honestly (folo walks a cursor) or has no
  server-side read state at all (x, reddit, douban, and bilibili keep their read
  markers in `feed.db`), and none of them is ever marked read at fetch time.

**x has two of them.** x's home has a Following timeline and a For You one, and
both are swept, cached and served here as sources in their own right: two chips
in the header wearing the same `𝕏`, blue for Following and black for For You.
Each has its own count, its own status light, its own briefing (see
**Summarizing a backlog**) and its own **mark all read**, and their items sit in
the merged feed together like everybody else's.

For You used to be the one chip that was not a backlog: a live scrape run inside
the request that tapped it, kept nowhere, so it could not be counted, summarized
or cleared. A briefing over a firehose is what makes the firehose usable — read
what the batch amounts to, then clear the rest in one tap — so it is a source
like the others now, and the terminal client's old `f` ("continue on x For You")
is gone with the live path: there is nothing to switch to, the items are already
in the feed.

Both timelines share one x session, one plugin (`--tab`) and one read state, so a
tweet you read under either chip is read in x. Overlap is filed once: the
timeline that cached a tweet first keeps it, and the other's sighting of the same
id is dropped rather than becoming a second card of the same post. `?app=xforyou`
is the chip's URL; the old `?x=foryou` still lands on it.

Run one active server for a synced snapshot; if two machines overwrite it, the
last completed fetch wins. SQLite keeps the full feed, blocked history, saved
items, and plugin read markers. The limits apply only to what one web response
sends to a browser.

### Blocking by keyword

Some things you don't want to triage, you want to never see. The header's
**N blocked** link opens a list of exactly that, and the **N keywords** link on
it opens the browser's own modal with the list of words in a textarea, one per
line. Edit, save, done — there is no per-row add and delete, because the list
already is a list.

A sweep screens what it fetched before the cache sees any of it: a post whose
**title** carries one of the words is kept out of `feed_items` and filed in
`blocked_items` instead, so it never becomes backlog you have to clear. Matching
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
because none of it was ever unread. **clear** in the header asks for
confirmation, then deletes the stored blocked history without changing the
keywords that continue screening later sweeps.

### One item, one URL

Every card's footer carries a **link** to that item on a page of its own,
`/item?app=<app>&id=<id>`. It is the same card with the same actions (save,
share, the player, the image toggle, the video **keep**) and no list around it,
which makes it something you can send to somebody, keep in a note, or open in a
tab and come back to after the feed has moved on.

The item is looked for in the backlog cache first, read or unread, so a URL does
not go dead the moment you scroll past the card. Failing that it comes from the
saved list, which is where an item lives on after the cache has pruned it, and
last from what the running server rendered, which covers a card the sweep has
pruned out from under the page it is on. An item none of them has answers
**404** rather than a blank card. `&json=1` returns that one item in the same
shape as the feed's JSON.

Nothing on the page is marked read by being looked at: arriving by URL is not
working through the feed, and a link that quietly emptied a slot of your backlog
would be a poor thing to hand around. Saving from it works as it does anywhere
else, and the header's **unread** link goes back to the feed.

### Summarizing a backlog

Every source chip with a backlog carries a ✦ beside it. Tapping it hands that
source's unread items to [Codex](https://github.com/openai/codex) —
`gpt-5.6-luna` at its default reasoning level — and nothing else happens to your
page: the icon starts spinning and you carry on reading. That is the point of it.
A run takes a minute or two, so waiting on a blank screen for one would be the
worst way to spend it, and several sources can be spinning at once.

The icon is the whole state of the thing. Outline is idle, a turning ring means
it is being written, and **filled in, in the accent colour**, means there is one
waiting. Only the icon changes: an accent border on the chip means that source is
the pick the page is narrowed to, and nothing else gets to say that. A toast tells
you when one lands while you are elsewhere. Tapping a filled icon opens what it
wrote: what the batch is mostly about, then themes as sections, and a list of at
most five worth opening. Every item the briefing names is a link to that item's
own page (see **One item, one URL**), so the summary is a way into the feed
rather than a substitute for it.

Each cited item gets a **row of its own** — a bullet carrying four links is a
row nobody can tell apart — and wears a short label rather than its own URL,
which matters most on x, where a post has no title to borrow.

**summarize in**, in settings, picks the language: English or 中文. It applies to
the next run, not the ones already written — a briefing is in the language it was
written in — so switching hands the sparkles back to offering a run, and
switching back finds the earlier briefings still where they were kept. Titles,
names and `@handles` are left as they are in either language, since a cited item
renamed in translation is one you cannot find again.

A briefing goes in place of the cards it is of, so opening one from another page
takes you to that source's own page first (`?app=reddit&summary=1`, which is a
URL you can keep). Tapping the icon again puts the cards back exactly as they
were — hidden, not reloaded, so the scroll position, the players and anything
part-expanded all survive the round trip. **again**, in the briefing's header,
spends a fresh run over the backlog as it stands now.

The runs are the server's, not the request's: closing the tab, following a link
or picking another chip does not abandon one, and reopening the feed finds the
icons as it left them. Only one runs at a time however many were asked for —
codex is a subprocess costing minutes and tokens, and a handful racing finishes
no sooner — so a chip may spin for a while waiting its turn. Needs the `codex`
CLI on the host's PATH and logged in; without it the icon says so and goes back
to idle.

Each briefing reads the source's **whole** backlog, oldest first — a summary of
the newest slice would have a hole in it that nothing on the page could tell you
about. There is no item limit; the only bound is a 700-character clip per item,
which keeps one long article from spending the whole prompt, so a prompt grows
with the count and nothing else. A few hundred posts is tens of thousands of
tokens (x at 303 items ≈ 36k, reddit at 380 ≈ 59k). A backlog deep enough to
outrun the model's context fails as a job, carrying whatever codex said about
it, rather than being quietly trimmed to fit.

Finished briefings are kept in the tab's `sessionStorage`, so toggling the two
views costs nothing and a reload still finds them — session rather than local
storage because a briefing is about a backlog as it stood, and a backlog is what
changes while a tab is closed. Nothing is marked read by being summarized: being
told what is in a batch is not having read it, and a briefing that emptied the
backlog behind itself would leave you unable to act on what it just told you.

Deciding it read enough is your move, and **mark all read** stays on screen for
it — the one control the briefing does not hide, still under the thumb in the
deck. A briefing only ever covers one source, so that is all the button clears:
it asks first, and names the source it is about to empty rather than saying "this
feed", because the other sources' backlogs are none of this briefing's business.

### Swipe mode

The same feed can be dealt as a deck instead of scrolled: one card at a time,
centered on the screen, with the rest stacked behind it.

Which of the two you get is **the browser's to choose**, not the server's, and
there's no flag for it. One server serves a phone and a desktop at once and they
don't want the same shape, so a flag could only ever be right about one of them.
The header's **▤ list** / **▭ deck** toggle switches it and the browser
remembers, the way it remembers the feed order (`?deck=1` and `?deck=0` are the
same thing by hand).

A browser that has never said gets **one guess from the device it is**: a coarse
pointer is a thumb, and a thumb wants a card at a time, so a phone opens the
deck and everything else opens the list. One tap settles it either way, for good.
The layout stays server-rendered — a deck card gets a longer text budget than
a listed one, and the clipping is done in Go — so switching is a page load, and
a browser that wants the deck spends one redirect on arriving at a bare URL.

Two controls sit halfway down the screen, at one edge. **&gt;** marks the card
read and deals the next one, and **&lt;** restores the previous card to tui's
unread backlog and walks back to it, as far as the first card, where it goes
dead. The setting behind the unread count, *place deck card controls on left*,
moves the pair to the other edge, and is remembered by that browser. This unread
state is local to tui; it does not ask the source service to reverse a read
already sent there. On a keyboard **→**/**←** do the same two things. Once the
deck is dealt out the controls go away and a note says so; where to go next is
the chip row, which the deck carries above it like the feed does.

Cards get a longer text budget here (one card owns the screen) but stay clipped
to roughly a screenful, so the footer actions — open, save, share, link, image
toggle, video controls, the video **keep** download — are always in reach
(scroll the row itself sideways when they outrun the screen).
Anything past the clip sits behind the same "+N words" toggle.

The muted **tick** in the bottom-right asks for confirmation, then clears the
whole current feed, including items beyond the 20-card window. A long press names
each icon for a browser without hover.
The saved and blocked lists are for looking back over rather than triage, so
they stay scrolling lists whatever the feed is set to, and the toggle isn't
drawn on them.

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

then restart `tui serve` and Allow the popup one last time. From then on every
build signs with the same identity, so the firewall rule keeps matching across
rebuilds. `make launcher` uses the cert automatically whenever it exists and
falls back to ad-hoc signing otherwise; if running `tui serve` from another Mac,
run `make signing-cert` there once too. If the CLI route fails, the GUI
equivalent is Keychain Access → Certificate Assistant → Create a Certificate
(name `tui-codesign`, Self-Signed Root, Code Signing), then Trust → Code
Signing: Always Trust.

## Layout

The main binary contains the feed service, both feed clients, and every app
plugin. `tui serve` runs each logged-in feed plugin's JSON and mark-read
commands behind one SQLite-backed backlog. Bare `tui` renders that backlog by
calling the service API; it never shells out to plugins or opens the database.

Each standalone app keeps its own `Makefile`, `README`, and `.env`, so it can
still build and run independently:

```sh
make x
./tui x
```

Each feed app implements the same normalized JSON and mark-read contract. The
service owns merging, persistence, scheduling, and retries; each app still owns
its source-specific session and API behavior.

See each app's README for its keys, configuration, and cookie-capture details.
