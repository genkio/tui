package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

// The model and the reasoning level are fixed rather than settings: a briefing
// over a few hundred short posts is the same job every time, and a knob here
// would only be a way to get a worse one. Medium is the default level, which is
// what this work wants — the reading is the cost, not the thinking.
const (
	summaryModel     = "gpt-5.6-luna"
	summaryReasoning = "medium"
	// A run reads a whole backlog and thinks about it, which is minutes rather
	// than the milliseconds every other route here answers in.
	summaryTimeout = 5 * time.Minute
	// Per-item text budget, and the only bound on how big a prompt gets: a
	// briefing covers the whole backlog, however deep, because a summary of part
	// of it is a summary that quietly left things out. This caps any one item at
	// about 820 characters of block, so the prompt grows with the count and
	// nothing else — a few hundred posts is tens of thousands of tokens.
	//
	// A backlog deep enough to outrun the model's context fails as a job, with
	// what codex said about it, rather than being silently trimmed to fit.
	summaryBodyRunes = 700
	// How many sources can be waiting on the one worker. There are only ever a
	// handful of apps, so this is a bound against a stuck queue rather than a
	// limit anyone should meet.
	summaryQueue = 12
	// ...and the one exception to "the whole backlog": the whole feed's briefing
	// reads at most this many items, taken from the end you are reading from. A
	// source's backlog is one service's day and is left whole; every source's at
	// once is the sum of all of them, which is the run most likely to outrun the
	// model, cost the most and say the least per token. What it read is exactly
	// what mark-all clears under it, so the cap never leaves you having cleared
	// something you were not told about.
	summaryAllCap = 200
	// How many finished briefings' ids to keep around for mark-all. jobs holds
	// only the latest run of a source, while a page holds the briefing it
	// rendered for as long as its tab is open: a re-run that is still going, or
	// one that failed, must not leave you unable to clear the briefing in front
	// of you. A handful of sources times a few runs each is the whole of it.
	summaryKept = 24
)

// The languages a briefing can be asked for, and how each one is asked for.
// Spelled out per language rather than left to "whichever the items are mostly
// in": a source's posts are rarely all one language, and which one a summary
// comes back in should be the reader's choice rather than a coin toss on the
// batch. Titles and handles are left alone either way, since a cited item that
// has been renamed in translation is one you cannot find again.
var summaryLangs = map[string]string{
	"en": "Write in English, whatever language the items themselves are in.",
	"zh": "Write in Simplified Chinese (简体中文), whatever language the items themselves are in.",
}

// What a request that names no language gets, and what an unknown one falls back
// to rather than being refused: a briefing in the wrong language is still worth
// more than an error where a briefing was asked for.
const summaryLangDefault = "en"

func summaryLang(want string) string {
	if _, ok := summaryLangs[want]; ok {
		return want
	}
	return summaryLangDefault
}

// summaryJob is one source's briefing, and the whole of what a client needs to
// know about it: the state machine its chip's icon draws, and the result once
// there is one. HTML is left out of the states listing (see states) — a client
// polling for what is ready has no use for every briefing's prose.
//
// Lang is which language it was written in, which the page checks against the
// language it is set to now: a briefing in the other one is not the one you
// asked for, so its chip goes back to offering a run.
//
// New is how much of the source's backlog the briefing never read, counted when
// a client asks rather than stored: a fetch lands every quarter of an hour, and
// a briefing that has been overtaken should say so instead of reading as the
// last word on a backlog that has moved on. seen is the ids it was written
// from, which is what makes that count honest — see feedCache.unreadNew.
// Backlog is how deep the pick was when the run started, set only when it was
// deeper than the briefing read (see summaryAllCap): "200 of 356" is the honest
// way to say that, and a briefing that read everything says nothing about it.
type summaryJob struct {
	State     string `json:"state"` // "running", "done" or "failed"
	Lang      string `json:"lang,omitempty"`
	Count     int    `json:"count,omitempty"`
	Backlog   int    `json:"backlog,omitempty"`
	New       int    `json:"new,omitempty"`
	HTML      string `json:"html,omitempty"`
	Err       string `json:"error,omitempty"`
	Generated string `json:"generated,omitempty"`
	seen      map[string]bool
}

// summaryAsk is what goes in the queue: what to write about and the language to
// write it in, since the language is chosen when the button is tapped and the
// run may not start for minutes. An id makes it one item's discussion rather
// than a source's backlog.
// asc is which end of the backlog the page is reading from, which is the end a
// capped briefing is taken from: told what the 200 you are about to read say,
// not what 200 you will not reach for days say.
type summaryAsk struct {
	app  string
	id   string
	lang string
	asc  bool
}

func (a summaryAsk) key() string { return summaryKey(a.app, a.id) }

// summaryKey is how a job is addressed: a source briefing goes under the source
// itself, since there is only ever one of those, and an item's under a key of
// its own. Prefixed rather than joined bare, so no id can collide with a source.
func summaryKey(app, id string) string {
	if id == "" {
		return app
	}
	return "item:" + app + ":" + id
}

// summarizer runs the briefings. They are jobs held here rather than work done
// inside a request for two reasons: a source's chip fires one and you carry on
// reading, so the request that started it is gone long before it finishes, and
// several can be asked for at once while only one should actually be running —
// codex is a subprocess costing minutes and tokens, and a handful of them racing
// each other finishes no sooner.
//
// codex is the CLI call, swapped out in tests, which have no business spending
// five minutes on a model to find out whether a handler validates its form. hn
// is the comment fetch an item's briefing starts with, swapped for the same
// reason; find is how an item is looked up, which the server widens past the
// backlog cache to the saved list.
type summarizer struct {
	codex func(ctx context.Context, prompt string) (string, error)
	hn    func(ctx context.Context, ref hnRef) (hnThread, error)
	find  func(app, id string, now time.Time) (core.Item, bool)
	cache *feedCache
	queue chan summaryAsk
	mu    sync.Mutex
	jobs  map[string]summaryJob
	// What finished briefings read, outliving the job that wrote them: keyed by
	// the run's own stamp, so mark-all under a briefing clears what that
	// briefing read rather than what the latest run of the same source did.
	read map[string]map[string]bool
	kept []string // read's keys, oldest first, for eviction
}

func newSummarizer(cache *feedCache) *summarizer {
	return &summarizer{
		codex: codexSummary,
		hn:    fetchHNThread,
		find: func(app, id string, now time.Time) (core.Item, bool) {
			return cache.item(app, id, now)
		},
		cache: cache,
		queue: make(chan summaryAsk, summaryQueue),
		jobs:  map[string]summaryJob{},
		read:  map[string]map[string]bool{},
	}
}

// serve is the one worker, taking the queue a source at a time until the server
// stops. Its context is the server's, not any request's: a briefing asked for
// from a chip has to outlive the tap that asked for it, since the whole point is
// to carry on reading while it runs.
func (s *summarizer) serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ask := <-s.queue:
			s.put(ask.key(), s.brief(ctx, ask))
		}
	}
}

// start puts an ask in the queue, unless one is already going for it. The
// running state is written before the queueing, so a control that has just been
// tapped reads as busy however long the queue is.
func (s *summarizer) start(app, id, lang string, asc bool) error {
	lang = summaryLang(lang)
	key := summaryKey(app, id)
	s.mu.Lock()
	if s.jobs[key].State == "running" {
		s.mu.Unlock()
		return nil // already going: the tap that started it says the same thing
	}
	s.jobs[key] = summaryJob{State: "running", Lang: lang}
	s.mu.Unlock()
	select {
	case s.queue <- summaryAsk{app: app, id: id, lang: lang, asc: asc}:
		return nil
	default:
		s.mu.Lock()
		delete(s.jobs, key)
		s.mu.Unlock()
		return errors.New("too many briefings are already waiting — let one finish")
	}
}

// brief is one run, of whichever kind was asked for.
func (s *summarizer) brief(ctx context.Context, ask summaryAsk) summaryJob {
	if ask.id != "" {
		return s.briefItem(ctx, ask)
	}
	return s.briefApp(ctx, ask)
}

// briefApp is a source's backlog as it stands when its turn comes round, not as
// it stood when the chip was tapped, since a queue can hold a source for
// minutes and the fresher list is the one worth reading.
func (s *summarizer) briefApp(ctx context.Context, ask summaryAsk) summaryJob {
	items, backlog := summaryItems(s.cache.unread(time.Now(), ""), ask.app, ask.asc)
	if len(items) == 0 {
		return summaryJob{State: "failed", Lang: ask.lang, Err: "nothing unread there any more"}
	}
	md, err := s.codex(ctx, summaryPrompt(ask.app, ask.lang, items))
	if err != nil {
		if ctx.Err() != nil {
			return summaryJob{State: "failed", Lang: ask.lang, Err: "the server stopped before the summary was written"}
		}
		return summaryJob{State: "failed", Lang: ask.lang, Err: err.Error()}
	}
	// By feed key rather than by id: ids collide between services (a numeric
	// inoreader id and an x post id), and the whole feed's briefing reads them
	// side by side.
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		seen[core.Key(it.App, it.ID)] = true
	}
	capped := 0
	if backlog > len(items) {
		capped = backlog
	}
	return summaryJob{
		State: "done", Lang: ask.lang, Count: len(items), Backlog: capped, seen: seen,
		// The same Markdown renderer the cards use, so a briefing's links, lists
		// and tables come out as the rest of the page's prose does and the model's
		// output cannot bring HTML of its own with it.
		HTML:      linkify(md),
		Generated: time.Now().UTC().Format(time.RFC3339),
	}
}

// briefItem is one card's discussion: what the room said under it, which is the
// part a Hacker News card does not carry. A story is read through its comments
// and a comment through its replies — the same run either way, over a different
// piece of the same tree.
//
// The fetch happens here rather than when the button is tapped, on the worker's
// turn: a thread is a megabyte of JSON over a public API, and the run behind it
// is minutes, so there is nothing to gain by getting it any earlier.
func (s *summarizer) briefItem(ctx context.Context, ask summaryAsk) summaryJob {
	fail := func(why string) summaryJob {
		return summaryJob{State: "failed", Lang: ask.lang, Err: why}
	}
	it, ok := s.find(ask.app, ask.id, time.Now())
	if !ok {
		return fail("that item is no longer here")
	}
	ref, ok := hnRefOf(it)
	if !ok {
		return fail("only Hacker News items carry a discussion to summarize")
	}
	th, err := s.hn(ctx, ref)
	if err != nil {
		if ctx.Err() != nil {
			return fail("the server stopped before the summary was written")
		}
		return fail(err.Error())
	}
	if th.Count == 0 {
		if ref.Kind == "comment" {
			return fail("nobody has replied to that comment yet")
		}
		return fail("nothing has been said under that story yet")
	}
	md, err := s.codex(ctx, itemSummaryPrompt(it, th, ask.lang))
	if err != nil {
		if ctx.Err() != nil {
			return fail("the server stopped before the summary was written")
		}
		return fail(err.Error())
	}
	return summaryJob{
		State: "done", Lang: ask.lang, Count: th.Count,
		HTML:      linkify(md),
		Generated: time.Now().UTC().Format(time.RFC3339),
	}
}

func (s *summarizer) put(key string, job summaryJob) {
	s.mu.Lock()
	s.jobs[key] = job
	if len(job.seen) > 0 && job.Generated != "" {
		stamped := key + "@" + job.Generated
		if _, held := s.read[stamped]; !held {
			s.kept = append(s.kept, stamped)
		}
		s.read[stamped] = job.seen
		for len(s.kept) > summaryKept {
			delete(s.read, s.kept[0])
			s.kept = s.kept[1:]
		}
	}
	s.mu.Unlock()
}

// job is one briefing in full. The key is the source for a backlog and
// summaryKey's for an item; the overtaken count is a backlog's business alone,
// and an item job carries no ids for it to be counted against.
func (s *summarizer) job(key string) (summaryJob, bool) {
	s.mu.Lock()
	j, ok := s.jobs[key]
	s.mu.Unlock()
	if !ok {
		return j, false
	}
	j.New = s.overtaken(key, j)
	return j, true
}

// overtaken is how far behind a finished briefing has fallen: the source's
// unread items it never read, which is a count of the backlog now against the
// ids the run went through. Counted on the way out rather than kept on the job,
// since it changes with every sweep and every mark while the job stands still.
// Item jobs have no ids to be counted against and fall out at the first test,
// which is right: a discussion is not a backlog and cannot be overtaken by one.
func (s *summarizer) overtaken(key string, j summaryJob) int {
	if j.State != "done" || s.cache == nil || len(j.seen) == 0 {
		return 0
	}
	if key == allApp {
		return s.cache.unreadNew("", j.seen) // every source, the way the briefing read them
	}
	return s.cache.unreadNew(key, j.seen)
}

// states is every job without the prose: what the chips and the cards' buttons
// need to draw themselves, which is polled while anything is running. Both kinds
// are in the one map, keyed as they are asked for, so a page watching a card and
// a page watching a source ask the same question.
func (s *summarizer) states() map[string]summaryJob {
	s.mu.Lock()
	out := make(map[string]summaryJob, len(s.jobs))
	for key, j := range s.jobs {
		j.HTML = ""
		out[key] = j
	}
	s.mu.Unlock()
	for key, j := range out {
		j.New = s.overtaken(key, j)
		out[key] = j
	}
	return out
}

// startSummary (POST /summarize) asks for one source's briefing and answers at
// once with the job, not the summary: the wait is minutes, and a chip that spins
// while you carry on reading somewhere else is the point of the thing.
//
// Nothing is marked read by being summarized. Having been told what is in a
// batch is not having read it, and a briefing that emptied the backlog behind
// itself would leave you unable to act on what it just told you.
// An id makes it one item's discussion instead — a Hacker News card, whose
// comments are the half of it the feed does not carry.
func startSummary(w http.ResponseWriter, r *http.Request, sum *summarizer) {
	app := strings.TrimSpace(r.FormValue("app"))
	if app == "" {
		http.Error(w, "missing app", http.StatusBadRequest)
		return
	}
	// Refused here rather than left to fail in the worker: a control should not
	// spin for a minute to be told there was nothing behind it.
	if id := strings.TrimSpace(r.FormValue("id")); id != "" {
		it, ok := sum.find(app, id, time.Now())
		if !ok {
			http.Error(w, "no such item: it is neither in the backlog nor saved", http.StatusNotFound)
			return
		}
		if _, ok := hnRefOf(it); !ok {
			http.Error(w, "only Hacker News items carry a discussion to summarize", http.StatusBadRequest)
			return
		}
		startAsk(w, r, sum, app, id)
		return
	}
	if items, _ := summaryItems(sum.cache.unread(time.Now(), ""), app, false); len(items) == 0 {
		http.Error(w, "nothing unread there to summarize", http.StatusNotFound)
		return
	}
	startAsk(w, r, sum, app, "")
}

// covered is what a finished briefing read, as feed keys, which is what mark-all
// clears under it. The stamp names which run is meant: the page sends back the
// one it rendered, and asking for a run this server no longer holds the ids of
// is a refusal rather than a different briefing's items cleared under you. A
// page from before briefings were stamped sends none, and gets the latest
// finished run, which is what it must have been looking at.
// Nil for a run that never finished or was never asked for.
func (s *summarizer) covered(key, gen string) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := s.read[key+"@"+gen]
	if gen == "" {
		if j, ok := s.jobs[key]; ok && j.State == "done" {
			seen = j.seen
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make(map[string]bool, len(seen))
	for k := range seen {
		out[k] = true
	}
	return out
}

// startAsk queues the run and answers with the job. Which language to write it
// in is the browser's setting, sent with the ask rather than kept here: one
// server serves whoever opens it, and the choice belongs to the person reading.
// Which end of the feed the page is reading from rides along for the same
// reason the language does: it is the browser's setting, and it decides which
// slice a capped briefing takes.
func startAsk(w http.ResponseWriter, r *http.Request, sum *summarizer, app, id string) {
	if err := sum.start(app, id, r.FormValue("lang"), r.FormValue("order") == "asc"); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	j, _ := sum.job(summaryKey(app, id))
	writeSummaryJSON(w, http.StatusAccepted, j)
}

// showSummary (GET /summarize) reports on the jobs: one of them in full with
// ?app= (and ?id= for an item's discussion), or every job's state without the
// prose, which is what a page asks for on load and polls while anything is
// spinning.
func showSummary(w http.ResponseWriter, r *http.Request, sum *summarizer) {
	q := r.URL.Query()
	if app := strings.TrimSpace(q.Get("app")); app != "" {
		j, ok := sum.job(summaryKey(app, strings.TrimSpace(q.Get("id"))))
		if !ok {
			http.Error(w, "no summary of that", http.StatusNotFound)
			return
		}
		writeSummaryJSON(w, http.StatusOK, j)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Jobs map[string]summaryJob `json:"jobs"`
	}{sum.states()})
}

func writeSummaryJSON(w http.ResponseWriter, code int, j summaryJob) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(j)
}

// summaryItems is one source's whole unread, oldest first: the briefing reads in
// publication order, because "what happened" is a sequence, and it reads all of
// it, because a briefing that covered the newest slice would be a briefing with
// a hole in it that nothing on the page could tell you about.
// The whole feed asks for no narrowing at all: every source's backlog, merged
// into one reading rather than briefed a service at a time. That one is capped
// (summaryAllCap) and takes its slice from the end the page is reading from, so
// the briefing is of the items you are about to reach; it reads them oldest
// first either way, because "what happened" is a sequence whichever end you
// started at. The second return is how deep the pick was, which is what makes
// "200 of 356" sayable.
func summaryItems(backlog []core.Item, app string, asc bool) ([]core.Item, int) {
	sel := feedSel{Kind: "app", Key: app}
	if app == allApp {
		sel = feedSel{}
	}
	items := selectItems(backlog, sel)
	deep := len(items)
	if app == allApp && deep > summaryAllCap {
		sortItems(items, asc)
		items = items[:summaryAllCap]
	}
	sortItems(items, true)
	return items, deep
}

// summaryPrompt writes the briefing request: the instructions, then the batch,
// one block per item. Every item carries the URL of its own page here, and the
// model is told to cite that rather than build a link — an id is the service's
// own string and is not something to have guessed at.
func summaryPrompt(app, lang string, items []core.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Summarize an unread feed backlog for the person who has to get through it.

%s. %d unread items follow, oldest first.

Answer from the items below alone: run no commands, open no files, fetch
nothing. Never invent an item, a link, a name or a fact that is not in them.

%s Leave titles, names and @handles as they are written rather than translating
them, so a cited item can still be found.

Write Markdown:

- Open with two or three sentences on what this batch is mostly about.
- Group it into themes as "## " sections, the biggest first. A theme may open
  with a sentence of its own, and then names its items one per bullet: the
  item's link first, then what it says. Never put more than one item in a
  bullet — a row carrying four links is a row nobody can tell apart. Cite the
  items that carry the theme rather than every one of them, at most eight to a
  theme, and say plainly when a chunk of the batch is noise not worth the time.
- End with a section for the few worth opening: at most five items, one to a
  bullet, one line each on why it earns the click. Head that section in the
  language you are writing in, like the rest of them.
- Cite every item you name as a Markdown link whose target is the "page" value
  given with it, verbatim: [a short label](page). The label is a handful of
  words you write naming what the item is — never the page value, never an id,
  never a bare URL. An item with no title of its own (an x post is all body)
  gets a few words of what it says, or the handle that posted it. One link to a
  bullet, and cite nothing else as a link.
- No preamble, no sign-off, no closing summary of the summary, no code fence
  around the whole thing, and no heading above level two.

`, summarySaying(app), len(items), summaryLangs[summaryLang(lang)])

	if app == allApp {
		b.WriteString(`This batch is every source at once. Group it by what the items are about
rather than by which service they came from — a section per service would be
the chip row over again. Where the same story ran in more than one place, say so
in the one theme instead of repeating it.

`)
	}

	for i, it := range items {
		fmt.Fprintf(&b, "--- item %d\n", i+1)
		if s := strings.TrimSpace(it.Source); s != "" {
			fmt.Fprintf(&b, "source: %s\n", s)
		}
		if a := strings.TrimSpace(it.Author); a != "" && a != strings.TrimSpace(it.Source) {
			fmt.Fprintf(&b, "author: %s\n", a)
		}
		if it.Age != "" {
			fmt.Fprintf(&b, "age: %s\n", it.Age)
		}
		if ty := itemType(it); ty != "text" {
			fmt.Fprintf(&b, "carries: %s\n", ty)
		}
		fmt.Fprintf(&b, "page: %s\n", itemHref(it.App, it.ID))
		// A post with no title of its own (x) has only a body, and one whose title
		// is that body over again would otherwise be printed twice.
		title := strings.TrimSpace(itemTitle(it))
		body := strings.TrimSpace(it.Body)
		if title != "" && title != body {
			fmt.Fprintf(&b, "title: %s\n", oneLine(title))
		}
		if it.Quote != nil {
			body = strings.TrimSpace(body + "\n\n" + it.Quote.Inline())
		}
		if body != "" {
			fmt.Fprintf(&b, "text: %s\n", clipRunes(body, summaryBodyRunes))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// itemSummaryPrompt writes the discussion request: what the card is, then every
// comment under it, depth-first. No links are asked for and none are wanted —
// this lands inside a card that already links the thread, and a handle is how
// you find a commenter again.
func itemSummaryPrompt(it core.Item, th hnThread, lang string) string {
	var b strings.Builder
	if th.Ref.Kind == "comment" {
		fmt.Fprintf(&b, "Summarize the replies to one Hacker News comment for the person reading it.\n\n")
		fmt.Fprintf(&b, "Thread: %s\n", oneLine(itemTitle(it)))
		if th.Author != "" {
			fmt.Fprintf(&b, "The comment, by %s:\n%s\n", th.Author, clipRunes(th.Text, hnTextRunes))
		}
		fmt.Fprintf(&b, "\n%d replies follow, depth-first, each with how deep under the comment it sits.\n\n", th.Count)
	} else {
		fmt.Fprintf(&b, "Summarize the Hacker News discussion under a story for someone deciding whether to open it.\n\n")
		title := th.Title
		if title == "" {
			title = itemTitle(it)
		}
		fmt.Fprintf(&b, "Story: %s\n", oneLine(title))
		if th.URL != "" {
			fmt.Fprintf(&b, "Article: %s\n", th.URL)
		}
		if th.Points > 0 {
			fmt.Fprintf(&b, "Points: %d\n", th.Points)
		}
		if th.Text != "" {
			fmt.Fprintf(&b, "The post itself, by %s:\n%s\n", th.Author, clipRunes(th.Text, hnTextRunes))
		}
		fmt.Fprintf(&b, "\n%d comments follow, depth-first, each with how deep in the reply tree it sits.\n\n", th.Count)
	}

	fmt.Fprintf(&b, `Answer from the comments below alone: run no commands, open no files, fetch
nothing. Never invent a comment, a name or a fact that is not in them.

%s Leave @handles and names as they are written rather than translating them.

Write Markdown, and keep the whole thing under 250 words — this is read on a
card, under the item it is about:

- Open with two or three sentences on what the discussion is actually about and
  where it came down.
- Then bullets: one per position, argument or correction that carries weight,
  each naming the handles that made it. Say plainly what is contested and what
  went unanswered.
- End with one line on whether the discussion is worth reading past the item
  itself, and why.
- No headings, no links, no preamble, no sign-off, no code fence around the
  whole thing.

`, summaryLangs[summaryLang(lang)])

	n := 0
	hnWrite(&b, th.Replies, 1, &n)
	return b.String()
}

// summarySaying names what the briefing is of, in the prompt's opening line.
func summarySaying(app string) string {
	if app == allApp {
		return "Every source at once: the whole feed"
	}
	return "Source: " + appSaying(app)
}

// oneLine keeps a title on the line the block gives it, so a title carrying
// newlines cannot look like the start of the next field.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + " […]"
	}
	return s
}

// codexSummary runs the prompt through the Codex CLI and returns the model's
// last message.
//
// The prompt goes in on stdin rather than as an argument: a few hundred posts
// runs to hundreds of kilobytes, which is an argument list no shell or exec is
// obliged to accept. The answer comes back through --output-last-message rather
// than off stdout, which also carries the CLI's own progress.
//
// It is given an empty temporary directory to work in and a read-only sandbox.
// There is nothing here for a model to run or edit — the whole job is in the
// prompt — and pointing it at the feed server's own working directory would be
// handing it a repository it has no business in.
func codexSummary(ctx context.Context, prompt string) (string, error) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return "", errors.New("codex is not on PATH: install the Codex CLI to summarize a backlog")
	}
	dir, err := os.MkdirTemp("", "tui-summary-")
	if err != nil {
		return "", fmt.Errorf("summary workspace: %w", err)
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "summary.md")

	ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "exec",
		"--model", summaryModel,
		"-c", "model_reasoning_effort="+summaryReasoning,
		"--sandbox", "read-only",
		"--cd", dir,
		"--skip-git-repo-check",
		"--ephemeral",
		"--color", "never",
		"--output-last-message", out,
		"-")
	cmd.Stdin = strings.NewReader(prompt)
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("the summary took longer than %s — try one source at a time", summaryTimeout)
		}
		return "", fmt.Errorf("codex failed: %s", codexTrouble(log.String(), err))
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return "", fmt.Errorf("codex wrote no summary: %s", codexTrouble(log.String(), err))
	}
	md := strings.TrimSpace(string(b))
	if md == "" {
		return "", errors.New("codex returned an empty summary")
	}
	return md, nil
}

// codexTrouble picks what to put in front of the reader when the CLI fails. Its
// own words say more than the exit status does — a stale login, an unknown model
// — so look for the line that names the trouble, fall back to its last one, and
// trim to what a toast can hold. The exit status is all that is left when it
// said nothing at all.
func codexTrouble(log string, err error) string {
	var last, blamed string
	for _, ln := range strings.Split(strings.TrimSpace(log), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		last = ln
		if blamed == "" && strings.Contains(strings.ToLower(ln), "error") {
			blamed = ln
		}
	}
	if blamed != "" {
		return clipRunes(blamed, 200)
	}
	if last == "" {
		return err.Error()
	}
	return clipRunes(last, 200)
}
