package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/genkio/tui/core"
)

// appColors are the chip colors for the merged feed, matching the terminal
// theme so a service is recognizable at a glance.
var appColors = map[string]string{
	"x":         "#4a9eff", // blue
	xForYouApp:  "#000000", // black, so x's two timelines are told apart
	"inoreader": "#ffb100", // amber
	"folo":      "#d07df0", // magenta
	"reddit":    "#ff6b33", // orange
	"douban":    "#00b51d", // douban green
	"bilibili":  "#fb7299", // bilibili pink
}

// x's two timelines wear the same glyph: they are one service, and the color is
// what says which of them a chip or a card came from.
var appLabels = map[string]string{
	"x":         "𝕏",
	xForYouApp:  "𝕏",
	"inoreader": "ino",
	"folo":      "folo",
	"reddit":    "rdt",
	"douban":    "dou",
	"bilibili":  "bili",
}

// runServer owns the feed backlog and serves its API and web interface. The
// default listener is 0.0.0.0:8080 so other tailnet devices can reach it.
//
// A page load reads the backlog cache off disk; it does not scrape anything. A
// background sweeper owns the fetching, on a jittered interval, and it is the
// only writer of that cache. Read marks land in the cache first and are carried
// to each app's own `--mark-read` in the background, so the TUI still agrees
// about what has been read. Only the all view is exposed.
//
// dev re-reads page.tmpl per request so template edits show up on refresh.
// drain lets the sweeper tell a service that cannot page (see drainApps) that
// what it handed over has been read, which is the only way to reach the rest of
// its backlog.
//
// The feed is served as a scrolling list or as a deck of one card at a time; see
// deckWanted for why that is the browser's to say and not a flag here.
func runServer(root, addr string, dev, drain bool, every time.Duration) error {
	devPath := ""
	if dev {
		devPath = filepath.Join(root, "cmd", "tui", "page.tmpl")
	}
	loader, err := newPageLoader(devPath)
	if err != nil {
		return err
	}
	db, syncPath, err := prepareFeedDB()
	if err != nil {
		return err
	}
	defer db.close()
	saved, err := loadSavedDB(db)
	if err != nil {
		return err
	}
	tags := loadTagsDB(db)
	block, err := loadBlockerDB(db)
	if err != nil {
		return err
	}
	rendered := newRenderedItems()
	cache, err := loadFeedCacheDB(db)
	if err != nil {
		return err
	}
	sum := newSummarizer(cache)
	// An item's discussion can be asked for from anywhere its card is drawn, and
	// the saved list outlives the backlog, so a briefing looks an item up the same
	// way its own page does rather than in the cache alone.
	sum.find = func(app, id string, now time.Time) (core.Item, bool) {
		return findItem(app, id, now, cache, saved, rendered)
	}
	digests, err := newDigestStore(db)
	if err != nil {
		return err
	}
	sum.digests = digests
	interests, err := loadInterestsDB(db)
	if err != nil {
		return err
	}
	sift := newSifter(cache, interests)
	flusher := newMarkFlusher(root, cache)
	ylen := newYTLens()
	sweep := newSweeper(root, cache, flusher, block, drain, every)
	sweep.ylen, sweep.saved = ylen, saved
	// Every fetch is followed by a sift of what it brought in, so the button is
	// a way of asking early rather than the only way of asking.
	sweep.sift = sift.auto
	// ...and by a summary of what is still unread, a couple of hundred items at
	// a time, until the backlog has been described and a fetch's own handful is
	// all a digest has left to say.
	sweep.digest = sum.digestAuto
	if syncPath != "" {
		sweep.after = func() error { return db.snapshot(syncPath) }
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	var workers sync.WaitGroup
	workers.Add(4)
	defer func() {
		stop()
		workers.Wait()
	}()
	go func() {
		defer workers.Done()
		flusher.run(ctx)
	}()
	go func() {
		defer workers.Done()
		sweep.run(ctx)
	}()
	// A briefing outlives the tap that asked for it, so it runs here rather than
	// inside the request: one at a time, on the server's own clock.
	go func() {
		defer workers.Done()
		sum.serve(ctx)
	}()
	// A sift is minutes of round trips over the whole backlog, so it runs out
	// here for the same reason a briefing does.
	go func() {
		defer workers.Done()
		sift.serve(ctx)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handleAll(w, r, root, loader, cache, sweep, saved, tags, block, rendered, sum, interests)
	})
	// POST puts the unjudged backlog through TypeSafe and GET reports on the
	// run. A POST because it spends an API key's tokens, so nothing should be
	// able to prefetch or replay it.
	mux.HandleFunc("/sift", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			startSift(w, sift)
		case http.MethodGet:
			showSift(w, sift)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/item", func(w http.ResponseWriter, r *http.Request) {
		handleItem(w, r, loader, cache, saved, block, rendered)
	})
	mux.HandleFunc("/mark", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleMark(w, r, cache, flusher)
	})
	mux.HandleFunc("/unmark", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleUnmark(w, r, cache)
	})
	mux.HandleFunc("/mark-all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleMarkAll(w, r, cache, flusher, sum)
	})
	// How the page knows when a fetch it asked for has finished: a sweep can
	// take minutes, so the alternative is guessing at a delay and reloading into
	// the same numbers.
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"fetching":%t,"unread":%d}`, sweep.sweeping(), cache.unreadCount())
	})
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleSave(w, r, saved, cache, rendered)
	})
	mux.HandleFunc("/tags", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleTag(w, r, tags, saved)
	})
	mux.HandleFunc("/keywords", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleKeywords(w, r, block, cache)
	})
	// POST asks for a source's briefing and GET reports on the ones asked for.
	// Starting one is a POST because it spends a subprocess and tokens, and
	// nothing should be able to prefetch or replay that.
	mux.HandleFunc("/summarize", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			startSummary(w, r, sum)
		case http.MethodGet:
			showSummary(w, r, sum)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	// The pile of digests a fetch writes by itself: GET reports on the run and
	// takes the language the page is reading in, POST is the two things that can
	// be done to the one on screen (next, retry).
	mux.HandleFunc("/digest", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			startDigest(w, r, sum, cache, flusher)
		case http.MethodGet:
			showDigest(w, r, sum)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	// The reader's own list of what they are following, which a sift asks about
	// every item. A POST because saving it drops every judgment in the backlog.
	mux.HandleFunc("/interests", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleInterests(w, r, interests, cache)
	})
	mux.HandleFunc("/blocked/clear", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleClearBlocked(w, block)
	})
	mux.HandleFunc("/pos", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handlePos(w, r, saved)
	})
	mux.HandleFunc("/dl", handleDownload)
	mux.HandleFunc("/img", handleImage)
	mux.HandleFunc("/ytlen", ylen.handle)
	mux.HandleFunc("/redgif", newRedgifLens().handle)
	mux.HandleFunc(biliPath, newBiliLens().handle)

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --addr %q: %w", addr, err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind %s: %w", addr, err)
	}
	defer ln.Close()

	fmt.Printf("tui serve listening on %s\n", addr)
	if u := tailscaleURL(host, port); u != "" {
		fmt.Printf("  tailnet:  %s\n", u)
	}
	if every > 0 {
		fmt.Printf("  fetching every %s (±%d%%) into %s\n", every, int(sweepJitter*100), cache.path)
	} else {
		fmt.Printf("  fetching on demand only into %s\n", cache.path)
	}
	if syncPath != "" {
		fmt.Printf("  syncing after each fetch to %s\n", syncPath)
	}
	if drain {
		fmt.Println("  draining: a fetched Inoreader article is marked read there so the rest of the backlog can be reached")
	}
	if dev {
		fmt.Printf("  dev: edit %s and refresh\n", devPath)
	}
	fmt.Println("  (ctrl-c to stop)")

	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// tailscaleURL returns the device's Tailscale IPv4 URL for port when tailscale
// is on PATH, so the user has a URL they can open from another device.
func tailscaleURL(host, port string) string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		if out, err := exec.Command(p, "ip", "-4").Output(); err == nil {
			if ip := strings.Fields(string(out)); len(ip) > 0 {
				if !tailnetReachable(host, ip[0]) {
					return ""
				}
				return "http://" + ip[0] + ":" + port + "/"
			}
		}
	}
	return ""
}

func tailnetReachable(host, tailnetIP string) bool {
	switch host {
	case "", "0.0.0.0", "::":
		return true
	default:
		return host == tailnetIP
	}
}

// authedFeedApps lists the logged-in apps the all view merges, freshly computed
// so a login picked up while the server runs is reflected on the next load.
//
// x counts twice: its For You timeline is a source of its own here (see
// xForYouApp), swept and served like any other, and it is logged in exactly when
// x is because it is x.
func authedFeedApps(root string) []string {
	var out []string
	for _, a := range appsIn(root) {
		if !a.feed || !a.authed() {
			continue
		}
		out = append(out, a.name)
		if a.name == "x" {
			out = append(out, xForYouApp)
		}
	}
	return out
}

const (
	listWindow = 100
	deckWindow = 20
	// How much of what you have already read the deck carries behind the first
	// unread card. A deck that runs out reloads for the next window, and without
	// this the reload would throw away every card you could walk back to: one
	// window's worth of history makes the back arrow reach the last thing you
	// read, whichever side of the page turn it fell on. The list has no need for
	// it — a read card stays in place there, greyed, and scrolling up is the way
	// back.
	deckHistory = deckWindow
)

func clientWindow(deck bool) int {
	if deck {
		return deckWindow
	}
	return listWindow
}

// deckBehind is the deck's history tail for this page: the last items read out
// of the same pick the page is showing, oldest read first. Empty for the list,
// which does not need it.
func deckBehind(cache *feedCache, now time.Time, deck bool, sel feedSel, want func(*feedEntry) bool) []core.Item {
	if !deck {
		return nil
	}
	// Narrowed after the fact rather than inside the pick: the cap is on what the
	// page carries, so a chip's history has to be counted in that chip's items.
	items := cache.lastRead(now, want)
	// The gist pile is picked by want, not by anything on the item: what put it
	// there is a request you made, which selectItems has no way to see.
	if sel.Kind != "gist" {
		items = selectItems(items, sel)
	}
	if len(items) > deckHistory {
		items = items[len(items)-deckHistory:]
	}
	return items
}

// handleAll renders the all timeline as a mobile-friendly HTML page (or JSON
// with ?json=1), served from the backlog cache rather than a fetch. Default
// order is oldest-first; ?order=desc flips to newest-first, which is what the
// header's toggle asks for — the page carries a window of the backlog, so the
// sorting has to happen here, over the whole of it.
//
// A chip in the header narrows the page to one source (?app=reddit) or one kind
// of thing (?type=video) — one at a time, which is why it is a page load and not
// a matter of hiding cards. Nothing here fetches: x's For You used to be served
// live from inside this request and is now a swept source like the rest
// (xForYouApp), which is what makes it summarizable and clearable.
func handleAll(w http.ResponseWriter, r *http.Request, root string, loader *pageLoader, cache *feedCache, sweep *sweeper, saved *savedStore, tags *tagStore, block *blocker, rendered *renderedItems, sum *summarizer, interests *interestStore) {
	// In --dev a template typo should show up immediately, so load (and in dev,
	// re-parse) the template first.
	tmpl, err := loader.load()
	if err != nil {
		http.Error(w, "page template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	q := r.URL.Query()
	order := feedOrder(q.Get("order")) // oldest first by default
	// Which layout this client wants: one server serves a phone and a desktop at
	// once, and they don't want the same shape.
	deck := deckWanted(q)
	sel := parseSel(q)

	// The saved view reads straight from the store: no fetch, so it loads
	// instantly and still works when a service (or the network) is down.
	if q.Get("saved") == "1" {
		compact := q.Get("compact") == "1"
		all := saved.list(now) // newest save first; publish order is not what you saved for
		itemTags, err := tags.all()
		if err != nil {
			http.Error(w, "tags: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tag := q.Get("tag")
		if tag != "untagged" && !validSavedTag(tag) {
			tag = ""
		}
		tally := tallyItems(all)
		items := selectItems(all, sel)
		items = filterSavedByTag(items, itemTags, tag)
		if q.Get("json") == "1" {
			w.Header().Set("Content-Type", "application/json")
			writeJSONItems(w, items, nil, feedAPIResponse{})
			return
		}
		writePage(w, tmpl, pageInput{
			items: items, total: len(items), now: now, saved: saved, savedView: true,
			savedCompact: compact, block: block, swipe: deck, sel: sel, tally: &tally, query: q,
			tags: itemTags, tagFilters: savedTagFilters(all, itemTags, tag, q),
		})
		return
	}

	// The blocked view, likewise off disk: what the keywords kept out of the
	// feed, newest block first, as titles alone.
	if q.Get("blocked") == "1" {
		all := block.list(now)
		tally := tallyItems(all)
		items := selectItems(all, sel)
		if q.Get("json") == "1" {
			w.Header().Set("Content-Type", "application/json")
			writeJSONItems(w, items, nil, feedAPIResponse{})
			return
		}
		writePage(w, tmpl, pageInput{
			items: items, total: len(items), now: now, block: block, blockedView: true,
			sel: sel, tally: &tally, query: q,
		})
		return
	}

	apps := authedFeedApps(root)

	// The skipped view: what the sift set aside, read the way the feed is read.
	// Not a list to look back over like the saved and blocked ones — it is the
	// backlog minus the part a model thinks you can skip, and the only way to
	// find out whether it was right is to go through it. So: cards in full, the
	// deck, the sort toggle, mark-all, and reading one marks it read for real.
	if q.Get("skipped") == "1" {
		all, worth := cache.skipped(now)
		tally := tallyItems(all)
		items := selectItems(all, sel)
		// Best first here is the borderline ones first, which are exactly the
		// judgments most likely to have been wrong.
		sortFeed(items, order, worth)
		if q.Get("json") == "1" {
			rendered.put(items)
			w.Header().Set("Content-Type", "application/json")
			writeJSONItems(w, items, nil, feedAPIResponse{Apps: apps})
			return
		}
		total := len(items)
		if window := clientWindow(deck); len(items) > window {
			items = items[:window]
		}
		behind := deckBehind(cache, now, deck, sel, func(e *feedEntry) bool { return e.skipped() })
		rendered.put(items)
		rendered.put(behind)
		writePage(w, tmpl, pageInput{
			items: items, behind: behind, total: total, apps: apps, now: now, sel: sel, tally: &tally,
			query: q, saved: saved, block: block, swipe: deck, order: order,
			skippedView: true, worth: worth,
		})
		return
	}

	// The whole backlog, whichever chip is on: it is what the chips count, so
	// every one of them still says what picking it would bring.
	backlog := cache.unread(now, "")
	tally := tallyItems(backlog)
	// The gist pile is a pick of its own rather than a flag over the backlog:
	// asking for a discussion takes the item out of the feed, so the chip counts
	// a pile the other chips no longer hold. Its items carry whether the thread
	// has actually been read yet, which is what decides whether a card opens on
	// arrival there or says it is still reading.
	gisting := cache.gisting(now)
	if gists := sum.gisted(); len(gists) > 0 {
		for i := range gisting {
			gisting[i].Gisted = gists[core.Key(gisting[i].App, gisting[i].ID)]
		}
	}
	tally.gists = len(gisting)
	// Likewise the digests a fetch wrote by itself: a pile of its own, counted
	// off the store rather than out of the backlog, since a digest is prose
	// about items rather than an item.
	tally.digests = sum.digests.waitingCount()

	items := selectItems(backlog, sel)
	// What the deck's back arrow can reach: the last items read out of whichever
	// pile this page is, since the two are read separately.
	behindWant := func(e *feedEntry) bool { return !e.skipped() && !e.gisting() }
	if sel.Kind == "gist" {
		items = gisting
		behindWant = func(e *feedEntry) bool { return e.gisting() }
	}
	// The summary chip is prose rather than cards: one digest on screen, the
	// oldest one nobody has cleared, with next and retry under it. No items, so
	// nothing here counts it or windows it.
	var digest *digestData
	digesting := sel.Kind == "digest"
	running := false
	if j, ok := sum.job(digestKey); ok && j.State == "running" {
		running = true
	}
	if digesting {
		items = nil
		if d, ok := sum.digests.waiting(); ok {
			digest = &digestData{
				ID: d.ID, Count: d.Count, Waiting: tally.digests, HTML: template.HTML(d.HTML),
				Stamp: d.Generated, Running: running,
			}
			if at, err := time.Parse(time.RFC3339, d.Generated); err == nil {
				digest.When = humanAgo(at)
			}
		}
	}
	failed, warn, capped := cache.trouble(apps)
	// Only the best-first order needs the numbers; the other two are a clock.
	var worth map[string]float64
	if order == orderBest {
		worth = cache.worths()
	}
	sortFeed(items, order, worth)

	if q.Get("json") == "1" {
		rendered.put(items)
		w.Header().Set("Content-Type", "application/json")
		meta := feedAPIResponse{
			Apps: apps, Warn: warn, Fetching: sweep.sweeping(), Capped: capped,
		}
		if updated := cache.sweptAt(); !updated.IsZero() {
			meta.Updated = updated.UTC().Format(time.RFC3339)
		}
		writeJSONItems(w, items, failed, meta)
		return
	}

	total := len(items)
	window := clientWindow(deck)
	if len(items) > window {
		items = items[:window]
	}
	behind := deckBehind(cache, now, deck, sel, behindWant)
	// Remember what this page showed so a save button can post back just an
	// app+id and still persist the whole item, even one the cache has since
	// pruned out from under the page it is on.
	rendered.put(items)
	rendered.put(behind)

	writePage(w, tmpl, pageInput{
		items: items, behind: behind, total: total, apps: apps, failed: failed, now: now,
		sel: sel, tally: &tally, query: q, warn: warn, saved: saved, block: block,
		swipe: deck, order: order, worth: worth, updated: cache.sweptAt(), fetching: sweep.sweeping(), capped: capped,
		summaryOpen: q.Get("summary") == "1", interests: interests.words(),
		digestView: digesting, digest: digest, digestRunning: running,
		sumLang: sum.digests.language(),
	})
}

// handleItem serves one item on a page of its own (/item?app=x&id=123), so a
// post has an address that outlives the page it was found on: one to send
// somebody, or to keep for something you want to come back to without hunting
// through the list it was in. The card is the same card, actions and all — save,
// share, the player — minus the list around it.
//
// Nothing here is marked read by being looked at: arriving by URL is not
// reading the feed, and a link that quietly emptied a slot of the backlog would
// be a poor thing to hand around.
func handleItem(w http.ResponseWriter, r *http.Request, loader *pageLoader, cache *feedCache, saved *savedStore, block *blocker, rendered *renderedItems) {
	q := r.URL.Query()
	app, id := q.Get("app"), q.Get("id")
	if app == "" || id == "" {
		http.Error(w, "missing app or id", http.StatusBadRequest)
		return
	}
	now := time.Now()
	it, ok := findItem(app, id, now, cache, saved, rendered)
	if !ok {
		http.Error(w, "no such item: it is neither in the backlog nor saved", http.StatusNotFound)
		return
	}
	if q.Get("json") == "1" {
		w.Header().Set("Content-Type", "application/json")
		writeJSONItems(w, []core.Item{it}, nil, feedAPIResponse{})
		return
	}
	tmpl, err := loader.load()
	if err != nil {
		http.Error(w, "page template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Same reason the feed does it: the save button posts back app+id alone, and
	// an item that reached this page from the render cache is not in the backlog
	// for the save to find again.
	rendered.put([]core.Item{it})
	writePage(w, tmpl, pageInput{
		items: []core.Item{it}, total: 1, now: now, saved: saved, block: block,
		itemView: true, query: q,
	})
}

// findItem looks one item up by app+id, wherever it still exists: the backlog
// cache first (read or unread, since a read item's URL should not go dead the
// moment you scroll past it), then the saved list, which is what an item falls
// back to once the cache has pruned it, and last what this process rendered,
// which is the only record of an item that was never cached at all (x's For
// You).
func findItem(app, id string, now time.Time, cache *feedCache, saved *savedStore, rendered *renderedItems) (core.Item, bool) {
	if it, ok := cache.item(app, id, now); ok {
		if !it.At.IsZero() {
			it.Age = humanAgo(it.At)
		}
		return it, true
	}
	if saved != nil {
		if it, ok := saved.item(app, id, now); ok {
			return it, true
		}
	}
	return rendered.get(app, id)
}

// writePage renders to a buffer first, so a template error mid-page becomes a
// clean 500 instead of half a document.
func writePage(w http.ResponseWriter, tmpl *template.Template, in pageInput) {
	var page bytes.Buffer
	if err := tmpl.Execute(&page, buildPageData(in)); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page.Bytes())
}

// handleSave stars or unstars one item. Saving needs the whole item, which the
// button doesn't carry, so it comes from the backlog cache, or from what this
// process last rendered when the cache no longer holds it. A miss in both means
// the page predates a restart, and the client is told to reload.
func handleSave(w http.ResponseWriter, r *http.Request, saved *savedStore, cache *feedCache, rendered *renderedItems) {
	app, id := r.FormValue("app"), r.FormValue("id")
	if app == "" || id == "" {
		http.Error(w, "missing app or id", http.StatusBadRequest)
		return
	}
	var err error
	if r.FormValue("save") == "1" {
		now := time.Now()
		it, ok := cache.item(app, id, now)
		if !ok {
			it, ok = rendered.get(app, id)
		}
		if !ok {
			http.Error(w, "item is no longer in view; reload the page", http.StatusConflict)
			return
		}
		err = saved.add(it, now)
	} else {
		err = saved.remove(app, id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"saved":%d}`, saved.count())
}

func handleTag(w http.ResponseWriter, r *http.Request, tags *tagStore, saved *savedStore) {
	app, id, tag := r.FormValue("app"), r.FormValue("id"), r.FormValue("tag")
	if app == "" || id == "" {
		http.Error(w, "missing app or id", http.StatusBadRequest)
		return
	}
	if !validSavedTag(tag) {
		http.Error(w, "unknown tag", http.StatusBadRequest)
		return
	}
	onValue := r.FormValue("on")
	if onValue != "0" && onValue != "1" {
		http.Error(w, "on must be 0 or 1", http.StatusBadRequest)
		return
	}
	if !saved.has(app, id) {
		http.Error(w, errTagItemNotSaved.Error(), http.StatusConflict)
		return
	}
	on := onValue == "1"
	if err := tags.set(app, id, tag, on, time.Now()); err != nil {
		if errors.Is(err, errTagItemNotSaved) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"tag":%q,"on":%t}`, tag, on)
}

// handleKeywords replaces the block list with what the modal's textarea holds,
// one keyword per line. Saving also re-screens the backlog that is already
// cached: a word is added because of something you are looking at right now, so
// leaving that very thing on the page is the one outcome it can't have.
func handleKeywords(w http.ResponseWriter, r *http.Request, block *blocker, cache *feedCache) {
	words := parseKeywords(r.FormValue("words"))
	if err := validKeywords(words); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := block.setKeywords(words); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	moved, err := block.purge(cache, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"keywords":%d,"blocked":%d,"moved":%d}`, len(words), block.count(), moved)
}

func handleClearBlocked(w http.ResponseWriter, block *blocker) {
	cleared, err := block.clearItems()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"cleared":%d}`, cleared)
}

// maxPosSrc caps the stream URL a position is pinned to, so a stray post can't
// grow the store.
const maxPosSrc = 2048

// handlePos remembers how far into a saved item's player the page has got, so
// the next visit picks up there — on this device or on another one reading the
// same synced store. Reported while playing and again on pause or as the page
// goes away, which is also what a sendBeacon lands on. An item that isn't saved
// is not an error: the page reports from the feed too, and there the position
// has nothing to live on.
func handlePos(w http.ResponseWriter, r *http.Request, saved *savedStore) {
	app, id := r.FormValue("app"), r.FormValue("id")
	if app == "" || id == "" {
		http.Error(w, "missing app or id", http.StatusBadRequest)
		return
	}
	secs, err := strconv.ParseFloat(r.FormValue("secs"), 64)
	if err != nil || math.IsNaN(secs) || math.IsInf(secs, 0) || secs < 0 {
		http.Error(w, "secs must be a non-negative number", http.StatusBadRequest)
		return
	}
	src := r.FormValue("src")
	if len(src) > maxPosSrc {
		http.Error(w, "src too long", http.StatusBadRequest)
		return
	}
	kept, err := saved.setPos(app, id, src, secs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":%t}`, kept)
}

// The three ways the feed can run. Oldest first is the default and the way a
// backlog is worked through; newest first is the other half of that toggle; best
// is the sift's own order — what it thought most worth your time at the top,
// which is how you read the good half of a backlog and stop wherever you like.
const (
	orderAsc  = "asc"
	orderDesc = "desc"
	orderBest = "best"
)

func feedOrder(want string) string {
	switch want {
	case orderDesc, orderBest:
		return want
	default:
		return orderAsc
	}
}

// sortFeed puts a page in the order the header asks for.
func sortFeed(items []core.Item, order string, worth map[string]float64) {
	if order != orderBest {
		sortItems(items, order != orderDesc)
		return
	}
	// By time first, so items the sift thought equally of keep the feed's own
	// order among themselves rather than whatever the map iterated in.
	sortItems(items, true)
	sort.SliceStable(items, func(i, j int) bool {
		return itemWorth(items[i], worth) > itemWorth(items[j], worth)
	})
	// Then by rung, which is the coarser and better-founded of the two signals:
	// a ladder with described rungs says more about what to read next than a
	// yes/no probability does, and the yes/no is what orders a rung's own items
	// among themselves. An item nothing has judged sits with the middle rung
	// rather than under everything.
	sort.SliceStable(items, func(i, j int) bool {
		return itemRank(items[i]) > itemRank(items[j])
	})
}

// itemRank is the rung an item sits on, with one nothing has judged put in the
// middle of the ladder: unknown is not the bottom of it.
func itemRank(it core.Item) int {
	if it.Rank < 1 {
		return (len(siftLevels) + 1) / 2
	}
	return it.Rank
}

// itemWorth is what the sift gave an item, with one it has never reached
// counted as neither good nor bad: an item that arrived after the last run is
// unknown, and sinking it under everything that has been judged would be reading
// a missing answer as a low one.
func itemWorth(it core.Item, worth map[string]float64) float64 {
	if w, ok := worth[core.Key(it.App, it.ID)]; ok {
		return w
	}
	return siftUnknown
}

// sortItems orders the feed by publish time: oldest first when asc is true,
// newest first otherwise. Items without a resolvable time sink to the bottom.
func sortItems(items []core.Item, asc bool) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].At, items[j].At
		switch {
		case a.IsZero() && b.IsZero():
			return false
		case a.IsZero():
			return false
		case b.IsZero():
			return true
		default:
			if asc {
				return a.Before(b)
			}
			return a.After(b)
		}
	})
}

// handleMark marks one or more items read. The cache takes the mark and the
// request returns: carrying it to the app itself is the flusher's job, retried
// until it lands, because a service that spends a round trip per id cannot
// answer for a few hundred of them inside one request. An id the cache never
// saw goes straight to the flusher's own queue instead.
func handleMark(w http.ResponseWriter, r *http.Request, cache *feedCache, flusher *markFlusher) {
	app := r.FormValue("app")
	ids := r.Form["id"] // one or many 'id' values
	clean := ids[:0]
	for _, id := range ids {
		if id != "" {
			clean = append(clean, id)
		}
	}
	if app == "" || len(clean) == 0 {
		http.Error(w, "missing app or id", http.StatusBadRequest)
		return
	}
	unknown := cache.markRead(app, clean, time.Now())
	if err := cache.save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	flusher.push(app, unknown) // also kicks the flush for what the cache took
	flusher.kick()
	if r.FormValue("json") == "1" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true}`)
		return
	}
	// Referer-safe: go back where we came from, else the home page.
	back := r.Referer()
	if back == "" || !strings.HasPrefix(back, "/") {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func handleUnmark(w http.ResponseWriter, r *http.Request, cache *feedCache) {
	app, id := r.FormValue("app"), r.FormValue("id")
	if app == "" || id == "" {
		http.Error(w, "missing app or id", http.StatusBadRequest)
		return
	}
	changed := cache.markUnread(app, id)
	if changed {
		if err := cache.save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"changed":%t}`, changed)
}

func handleMarkAll(w http.ResponseWriter, r *http.Request, cache *feedCache, flusher *markFlusher, sum *summarizer) {
	if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	var items []core.Item
	// With a briefing open, "all" is what that briefing read and nothing else:
	// the whole feed's is capped, so the pick behind it can hold items it never
	// mentioned, and clearing those would be clearing what you were never told
	// about. Items that arrived since are left unread for the same reason.
	if brief := strings.TrimSpace(r.FormValue("brief")); brief != "" {
		covered := sum.covered(brief, strings.TrimSpace(r.FormValue("gen")))
		if covered == nil {
			http.Error(w, "the server no longer has that briefing — summarize again to clear what it reads", http.StatusNotFound)
			return
		}
		for _, it := range cache.unread(time.Now(), "") {
			if covered[core.Key(it.App, it.ID)] {
				items = append(items, it)
			}
		}
	} else if r.FormValue("skipped") == "1" {
		// In the skipped view "all" is that pile, not the feed behind it. The two
		// are disjoint by construction, and clearing the wrong one of them is
		// exactly the mistake worth being careful about here.
		aside, _ := cache.skipped(time.Now())
		items = selectItems(aside, parseSel(r.Form))
	} else if sel := parseSel(r.Form); sel.Kind == "gist" {
		// Likewise the gist pile: it is out of the feed, so clearing it here must
		// not reach the feed and clearing the feed must not reach it.
		items = cache.gisting(time.Now())
	} else {
		items = selectItems(cache.unread(time.Now(), ""), sel)
	}
	byApp := map[string][]string{}
	for _, it := range items {
		byApp[it.App] = append(byApp[it.App], it.ID)
	}
	for app, ids := range byApp {
		cache.markRead(app, ids, time.Now())
	}
	if len(items) > 0 {
		if err := cache.save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		flusher.kick()
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"marked":%d}`, len(items))
}

// handleDownload streams an x video through the server with an attachment
// disposition: browsers ignore the download attribute on cross-origin links,
// and video.twimg.com rejects requests that carry a referer, so a same-origin
// proxy is the only way a tap can save the mp4 with a proper filename.
func handleDownload(w http.ResponseWriter, r *http.Request) {
	u, err := parseVideoURL(r.URL.Query().Get("u"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng) // let download managers resume
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "fetch video: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		http.Error(w, "video source: "+resp.Status, http.StatusBadGateway)
		return
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+dlName(r.URL.Query().Get("n"))+`"`)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// imageUA is the browser this server claims to be when fetching a picture.
// doubanio turns away a bare Go client the same way it turns away a request
// with no Referer, so both have to look like the page load they stand in for.
const imageUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

// handleImage fetches a picture the browser is not allowed to ask for itself.
// doubanio answers only a request whose Referer is a douban page — a bare one
// gets 418, this server's own origin gets 418 — and a page cannot forge a
// Referer, so the fetch happens here and the bytes are passed on. No session
// cookie goes with it: these files are public.
func handleImage(w http.ResponseWriter, r *http.Request) {
	u, err := parseImageURL(r.URL.Query().Get("u"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req, err := imageRequest(r.Context(), u)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "fetch image: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "image source: "+resp.Status, http.StatusBadGateway)
		return
	}
	for _, h := range []string{"Content-Type", "Content-Length"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	// these files never change under their URL, so let the browser keep them
	// and spend no second round trip on a scroll back up
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.Copy(w, resp.Body)
}

// imageRequest builds the fetch that stands in for the page load the browser
// would have made. Both headers are load-bearing: doubanio answers 418 to a
// request with no Referer and 418 again to one wearing Go's own user agent, so
// dropping either brings the pictures back down.
func imageRequest(ctx context.Context, u *url.URL) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://www.douban.com/")
	req.Header.Set("User-Agent", imageUA)
	req.Header.Set("Accept", "image/avif,image/webp,image/*,*/*;q=0.8")
	return req, nil
}

// parseImageURL admits only douban's image CDN, so /img can't be used as an
// open proxy.
func parseImageURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Host, ".doubanio.com") {
		return nil, errors.New("u must be an https://*.doubanio.com/... URL")
	}
	return u, nil
}

// parseVideoURL admits only x's video CDN, so /dl can't be used as an open proxy.
func parseVideoURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "video.twimg.com" {
		return nil, errors.New("u must be an https://video.twimg.com/... URL")
	}
	return u, nil
}

var reDlName = regexp.MustCompile(`[^\w.-]+`)

// dlName sanitizes the requested filename to a safe attachment name.
func dlName(n string) string {
	n = reDlName.ReplaceAllString(n, "")
	if n == "" || n == ".mp4" {
		n = "video.mp4"
	}
	return n
}

// escape is a shorthand for html.EscapeString in templates below.
func escape(s string) string { return html.EscapeString(s) }
