package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

// A digest is the briefing nobody tapped for. A fetch lands every quarter of an
// hour and is sifted on the spot (see sifter.auto); this is the other half of
// that: the next couple of hundred unread items are read by the model as they
// arrive, so a backlog that took a week to pile up is described by the time you
// open it rather than a minute or two after you ask.
//
// It is a pile rather than one rolling summary, and that is what makes it
// finishable. Each run takes the oldest unread items no digest has read yet, up
// to digestCap, and files what came back under the summary chip. A backlog of
// fifteen hundred is eight fetches' worth of digests; once it is caught up, a
// fetch's own handful is one small digest and the chip is a couple of minutes'
// reading rather than a list to work through.
//
// Only two things can be done to one, which is the point of it: next clears
// every item it read and hands you the one behind it, and retry writes the same
// batch again. There is nothing to scroll, nothing to pick, and no way to end up
// half way through a digest wondering which of its items you have dealt with.
const (
	// The job key a digest run is held under, and the chip it fills. Like allApp
	// it is not a service and never will be, so it cannot collide with one.
	digestKey = "digest"
	// How many items one digest reads: the same bound the whole feed's briefing
	// runs under (summaryAllCap), for the same reason — every source's backlog
	// added together is the widest read there is, and the run most likely to
	// outrun the model. What is left over is the next fetch's digest.
	digestCap = summaryAllCap
)

// digestItem is one item a digest read, as the pair that names an item
// everywhere else here. Kept rather than the feed key, since clearing a digest
// is marking these read app by app.
type digestItem struct{ App, ID string }

// storedDigest is one digest: the prose, and the batch behind it. Marked is
// when next cleared it — the chip counts the ones where it is still empty,
// while a cleared one stays on disk because what it read is what keeps the next
// run from summarizing the same items over again.
type storedDigest struct {
	ID        int64
	Lang      string
	HTML      string
	Count     int
	Generated string
	Marked    string
	Items     []digestItem
}

func (d *storedDigest) clone() storedDigest {
	out := *d
	out.Items = append([]digestItem(nil), d.Items...)
	return out
}

// digestStore is every digest there has been, oldest first, with the language
// the automatic runs are written in. A store with no database behind it (tests)
// keeps them in memory and hands out ids of its own.
type digestStore struct {
	mu   sync.Mutex
	db   *feedDB
	all  []*storedDigest
	lang string
	next int64 // the id to hand out when there is no database to ask
}

func newDigestStore(db *feedDB) (*digestStore, error) {
	s := &digestStore{db: db, lang: summaryLangDefault}
	if db == nil {
		return s, nil
	}
	all, err := db.loadDigests()
	if err != nil {
		return nil, err
	}
	s.all = all
	lang, err := db.loadDigestLang()
	if err != nil {
		return nil, err
	}
	if lang != "" {
		s.lang = summaryLang(lang)
	}
	return s, nil
}

// covered is every item any digest has read, cleared or not: what the next run
// leaves alone, so the pile works its way through the backlog instead of
// describing the same two hundred items every quarter of an hour.
func (s *digestStore) covered() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, d := range s.all {
		for _, it := range d.Items {
			out[core.Key(it.App, it.ID)] = true
		}
	}
	return out
}

// waiting is the digest on screen: the oldest one nobody has cleared, since the
// pile is read the way the backlog is, oldest first. Both of these answer for a
// server that keeps no digests at all, which is what a page render asks of.
func (s *digestStore) waiting() (storedDigest, bool) {
	if s == nil {
		return storedDigest{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.all {
		if d.Marked == "" {
			return d.clone(), true
		}
	}
	return storedDigest{}, false
}

func (s *digestStore) waitingCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.all {
		if d.Marked == "" {
			n++
		}
	}
	return n
}

func (s *digestStore) find(id int64) (storedDigest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.all {
		if d.ID == id {
			return d.clone(), true
		}
	}
	return storedDigest{}, false
}

// add files a finished run. The batch goes in whatever the prose came to: a
// digest that read two hundred items has covered them either way, and a second
// run over the same items is a retry rather than an accident.
func (s *digestStore) add(job summaryJob, items []core.Item) error {
	d := &storedDigest{
		Lang: job.Lang, HTML: job.HTML, Count: job.Count, Generated: job.Generated,
	}
	for _, it := range items {
		d.Items = append(d.Items, digestItem{App: it.App, ID: it.ID})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		s.next++
		d.ID = s.next
		s.all = append(s.all, d)
		return nil
	}
	id, err := s.db.putDigest(d)
	if err != nil {
		return err
	}
	d.ID = id
	s.all = append(s.all, d)
	return nil
}

// replace is what a retry lands as: the same digest, in its place in the pile,
// with the prose written again.
func (s *digestStore) replace(id int64, job summaryJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.all {
		if d.ID != id {
			continue
		}
		d.Lang, d.HTML, d.Count, d.Generated = job.Lang, job.HTML, job.Count, job.Generated
		if s.db == nil {
			return nil
		}
		return s.db.updateDigest(d)
	}
	return errors.New("that summary is no longer here")
}

// mark clears one, and hands back the items it read for the caller to mark.
// False for a digest that is already cleared: two taps on next is one clear.
func (s *digestStore) mark(id int64, now time.Time) ([]digestItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.all {
		if d.ID != id || d.Marked != "" {
			continue
		}
		d.Marked = now.UTC().Format(time.RFC3339)
		if s.db != nil {
			_ = s.db.markDigest(d.ID, d.Marked)
		}
		return append([]digestItem(nil), d.Items...), true
	}
	return nil, false
}

// language is what the automatic runs are written in, and what the page checks
// its own setting against. Nil-safe: a render asks it of a server that may keep
// no digests at all.
func (s *digestStore) language() string {
	if s == nil {
		return summaryLangDefault
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lang
}

// setLanguage remembers what the page reading these is set to, so the runs a
// fetch starts are written in the language the person who will read them uses.
func (s *digestStore) setLanguage(lang string) {
	if s == nil {
		return
	}
	lang = summaryLang(lang)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lang == lang {
		return
	}
	s.lang = lang
	if s.db != nil {
		_ = s.db.saveDigestLang(lang)
	}
}

// digestAuto is the digest a fetch asks for, which is every digest there is
// until you tap retry: the sweeper calls it at the end of a sweep, behind the
// sift, so what has landed is described before anyone looks at it. A run
// already going, or a backlog every digest has already read, is the ordinary
// answer here and not worth a word.
func (s *summarizer) digestAuto() {
	if s.digests == nil || s.cache == nil {
		return
	}
	if s.ready != nil {
		if err := s.ready(); err != nil {
			s.saidPi.Do(func() { logf("summary: %v", err) })
			return
		}
	}
	if len(s.digestPick(time.Now())) == 0 {
		return
	}
	if err := s.start(summaryAsk{app: digestKey, lang: s.digests.language()}); err != nil {
		logf("summary: %v", err)
	}
}

// digestPick is what the next run reads: the unread backlog no digest has
// reached, oldest first, capped. Oldest first because the pile is worked
// through rather than dipped into — the digest in front of you is the oldest
// part of the backlog, which is the part that would otherwise never be read.
func (s *summarizer) digestPick(now time.Time) []core.Item {
	covered := s.digests.covered()
	var out []core.Item
	for _, it := range s.cache.unread(now, "") {
		if covered[core.Key(it.App, it.ID)] {
			continue
		}
		out = append(out, it)
	}
	sortItems(out, true)
	if len(out) > digestCap {
		out = out[:digestCap]
	}
	return out
}

// briefDigest is one automatic run, or one retry of an earlier one. It writes
// the result to the pile itself rather than leaving it in the job: a job is the
// latest run of a thing, and a digest has to outlive both the run and the
// server that made it.
func (s *summarizer) briefDigest(ctx context.Context, ask summaryAsk) summaryJob {
	fail := func(why string) summaryJob {
		return summaryJob{State: "failed", Lang: ask.lang, Err: why}
	}
	if s.digests == nil {
		return fail("this server keeps no summaries of its own")
	}
	var (
		items []core.Item
		redo  int64
	)
	if ask.redo != "" {
		id, err := strconv.ParseInt(ask.redo, 10, 64)
		if err != nil {
			return fail("no such summary")
		}
		d, ok := s.digests.find(id)
		if !ok {
			return fail("that summary is no longer here")
		}
		redo = d.ID
		keys := make(map[string]bool, len(d.Items))
		for _, it := range d.Items {
			keys[core.Key(it.App, it.ID)] = true
		}
		items = s.cache.byKeys(keys, time.Now())
		sortItems(items, true)
		if len(items) == 0 {
			return fail("none of what that summary read is still here")
		}
	} else {
		items = s.digestPick(time.Now())
		if len(items) == 0 {
			return fail("every unread item has been summarized already")
		}
	}
	// The whole feed's prompt: a digest is every source at once, grouped by what
	// the items are about rather than by which service they came from.
	md, err := s.ask(ctx, summaryPrompt(allApp, ask.lang, items))
	if err != nil {
		if ctx.Err() != nil {
			return fail("the server stopped before the summary was written")
		}
		return fail(err.Error())
	}
	job := summaryJob{
		State: "done", Lang: ask.lang, Count: len(items),
		HTML:      linkify(md),
		Generated: time.Now().UTC().Format(time.RFC3339),
	}
	var stored error
	if redo > 0 {
		stored = s.digests.replace(redo, job)
	} else {
		stored = s.digests.add(job, items)
	}
	if stored != nil {
		return fail(stored.Error())
	}
	return job
}

// showDigest (GET /digest) is what the page polls while a run is going.
func showDigest(w http.ResponseWriter, r *http.Request, sum *summarizer) {
	if sum.digests == nil {
		http.Error(w, "this server keeps no summaries of its own", http.StatusServiceUnavailable)
		return
	}
	job, _ := sum.job(digestKey)
	out := struct {
		Waiting   int    `json:"waiting"`
		State     string `json:"state,omitempty"`
		Err       string `json:"error,omitempty"`
		ID        int64  `json:"id,omitempty"`
		Generated string `json:"generated,omitempty"`
	}{Waiting: sum.digests.waitingCount(), State: job.State, Err: job.Err}
	if d, ok := sum.digests.waiting(); ok {
		out.ID, out.Generated = d.ID, d.Generated
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// startDigest (POST /digest) is the two things that can be done to the digest
// on screen. A POST either way: one of them clears a couple of hundred items
// and the other spends a subprocess and tokens, and nothing should be able to
// prefetch or replay that.
func startDigest(w http.ResponseWriter, r *http.Request, sum *summarizer, cache *feedCache, flusher *markFlusher) {
	if sum.digests == nil {
		http.Error(w, "this server keeps no summaries of its own", http.StatusServiceUnavailable)
		return
	}
	// The language the automatic runs are written in, which the page sends
	// whenever the setting changes and whenever it finds the server set to
	// something else. It names no digest — there may not be one yet — so it is
	// answered before the id is looked for.
	if r.FormValue("do") == "lang" {
		sum.digests.setLanguage(r.FormValue("lang"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"lang":%q}`, sum.digests.language())
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil {
		http.Error(w, "missing summary id", http.StatusBadRequest)
		return
	}
	switch r.FormValue("do") {
	case "next":
		nextDigest(w, sum, cache, flusher, id)
	case "retry":
		if _, ok := sum.digests.find(id); !ok {
			http.Error(w, "that summary is no longer here", http.StatusNotFound)
			return
		}
		lang := summaryLang(r.FormValue("lang"))
		sum.digests.setLanguage(lang)
		if err := sum.start(summaryAsk{app: digestKey, lang: lang, redo: strconv.FormatInt(id, 10)}); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		j, _ := sum.job(digestKey)
		writeSummaryJSON(w, http.StatusAccepted, j)
	default:
		http.Error(w, "do must be next, retry or lang", http.StatusBadRequest)
	}
}

// nextDigest clears one: every item it read is marked read, and the one behind
// it is what the page reloads into. An item already read is left alone — you
// may well have read a few of them in the feed since — and one the cache no
// longer holds goes straight to the flusher, so its own service still hears
// about it.
func nextDigest(w http.ResponseWriter, sum *summarizer, cache *feedCache, flusher *markFlusher, id int64) {
	items, ok := sum.digests.mark(id, time.Now())
	if !ok {
		http.Error(w, "that summary is no longer waiting", http.StatusNotFound)
		return
	}
	byApp := map[string][]string{}
	for _, it := range items {
		byApp[it.App] = append(byApp[it.App], it.ID)
	}
	for app, ids := range byApp {
		unknown := cache.markRead(app, ids, time.Now())
		if flusher != nil {
			flusher.push(app, unknown)
		}
	}
	if len(items) > 0 {
		if err := cache.save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if flusher != nil {
			flusher.kick()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		OK      bool `json:"ok"`
		Marked  int  `json:"marked"`
		Waiting int  `json:"waiting"`
	}{true, len(items), sum.digests.waitingCount()})
}
