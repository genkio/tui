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
type summaryJob struct {
	State     string `json:"state"` // "running", "done" or "failed"
	Lang      string `json:"lang,omitempty"`
	Count     int    `json:"count,omitempty"`
	HTML      string `json:"html,omitempty"`
	Err       string `json:"error,omitempty"`
	Generated string `json:"generated,omitempty"`
}

// summaryAsk is what goes in the queue: a source and the language to write about
// it in, since the language is chosen when the icon is tapped and the run may
// not start for minutes.
type summaryAsk struct {
	app  string
	lang string
}

// summarizer runs the briefings. They are jobs held here rather than work done
// inside a request for two reasons: a source's chip fires one and you carry on
// reading, so the request that started it is gone long before it finishes, and
// several can be asked for at once while only one should actually be running —
// codex is a subprocess costing minutes and tokens, and a handful of them racing
// each other finishes no sooner.
//
// codex is the CLI call, swapped out in tests, which have no business spending
// five minutes on a model to find out whether a handler validates its form.
type summarizer struct {
	codex func(ctx context.Context, prompt string) (string, error)
	cache *feedCache
	queue chan summaryAsk
	mu    sync.Mutex
	jobs  map[string]summaryJob
}

func newSummarizer(cache *feedCache) *summarizer {
	return &summarizer{
		codex: codexSummary,
		cache: cache,
		queue: make(chan summaryAsk, summaryQueue),
		jobs:  map[string]summaryJob{},
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
			s.put(ask.app, s.brief(ctx, ask))
		}
	}
}

// start puts a source in the queue, unless one is already going for it. The
// running state is written before the queueing, so a chip that has just been
// tapped reads as busy however long the queue is.
func (s *summarizer) start(app, lang string) error {
	lang = summaryLang(lang)
	s.mu.Lock()
	if s.jobs[app].State == "running" {
		s.mu.Unlock()
		return nil // already going: the tap that started it says the same thing
	}
	s.jobs[app] = summaryJob{State: "running", Lang: lang}
	s.mu.Unlock()
	select {
	case s.queue <- summaryAsk{app: app, lang: lang}:
		return nil
	default:
		s.mu.Lock()
		delete(s.jobs, app)
		s.mu.Unlock()
		return errors.New("too many briefings are already waiting — let one finish")
	}
}

// brief is one run: the backlog as it stands when its turn comes round, not as
// it stood when the chip was tapped, since a queue can hold a source for
// minutes and the fresher list is the one worth reading.
func (s *summarizer) brief(ctx context.Context, ask summaryAsk) summaryJob {
	items := summaryItems(s.cache.unread(time.Now(), ""), ask.app)
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
	return summaryJob{
		State: "done", Lang: ask.lang, Count: len(items),
		// The same Markdown renderer the cards use, so a briefing's links, lists
		// and tables come out as the rest of the page's prose does and the model's
		// output cannot bring HTML of its own with it.
		HTML:      linkify(md),
		Generated: time.Now().UTC().Format(time.RFC3339),
	}
}

func (s *summarizer) put(app string, job summaryJob) {
	s.mu.Lock()
	s.jobs[app] = job
	s.mu.Unlock()
}

func (s *summarizer) job(app string) (summaryJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[app]
	return j, ok
}

// states is every source's job without the prose: what the chips need to draw
// themselves, which is polled while anything is running.
func (s *summarizer) states() map[string]summaryJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]summaryJob, len(s.jobs))
	for app, j := range s.jobs {
		j.HTML = ""
		out[app] = j
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
func startSummary(w http.ResponseWriter, r *http.Request, sum *summarizer) {
	app := strings.TrimSpace(r.FormValue("app"))
	if app == "" {
		http.Error(w, "missing app", http.StatusBadRequest)
		return
	}
	// Refused here rather than left to fail in the worker: a chip should not spin
	// for a minute to be told there was nothing behind it.
	if len(summaryItems(sum.cache.unread(time.Now(), ""), app)) == 0 {
		http.Error(w, "nothing unread there to summarize", http.StatusNotFound)
		return
	}
	// Which language to write it in is the browser's setting, sent with the ask
	// rather than kept here: one server serves whoever opens it, and the choice
	// belongs to the person reading.
	if err := sum.start(app, r.FormValue("lang")); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	j, _ := sum.job(app)
	writeSummaryJSON(w, http.StatusAccepted, j)
}

// showSummary (GET /summarize) reports on the jobs: one source in full with
// ?app=, or every source's state without the prose, which is what a page asks
// for on load and polls while a chip is spinning.
func showSummary(w http.ResponseWriter, r *http.Request, sum *summarizer) {
	if app := strings.TrimSpace(r.URL.Query().Get("app")); app != "" {
		j, ok := sum.job(app)
		if !ok {
			http.Error(w, "no summary of that source", http.StatusNotFound)
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
func summaryItems(backlog []core.Item, app string) []core.Item {
	items := selectItems(backlog, feedSel{Kind: "app", Key: app})
	sortItems(items, true)
	return items
}

// summaryPrompt writes the briefing request: the instructions, then the batch,
// one block per item. Every item carries the URL of its own page here, and the
// model is told to cite that rather than build a link — an id is the service's
// own string and is not something to have guessed at.
func summaryPrompt(app, lang string, items []core.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Summarize an unread feed backlog for the person who has to get through it.

Source: %s. %d unread items follow, oldest first.

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

`, appLabel(app), len(items), summaryLangs[summaryLang(lang)])

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
