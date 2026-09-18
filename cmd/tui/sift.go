package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

// The sift is the other half of the briefing. A briefing tells you what is in
// the backlog; a sift takes the part of it that was never worth your time out
// of the way, so what is left is a backlog about the size of an evening.
//
// It runs on TypeSafe rather than on a chat model because of what it is: a few
// hundred yes/no judgments, one per item, with no prose anywhere in the answer.
// Jev ingests a batch once and answers every question against it in parallel,
// which is what makes the whole backlog affordable — where a briefing is one
// long prompt that has to fit in one context window, a sift is arithmetic over
// however many batches it takes.
//
// Nothing here is deleted and nothing is marked read. An item the sift sets
// aside leaves the feed for the skipped view, which is read exactly the way the
// feed is read: a judgment you disagree with is one scroll away from being read
// anyway, and only your reading of it ever marks it read.
const (
	typesafeURL   = "https://api.typesafe.ai/v1/systemone"
	typesafeModel = "jev-latest"
	typesafeKey   = "TYPESAFE_API_KEY"
	// Where the line falls: an item is set aside when the model puts its chance
	// of being worth reading below this. Deliberately low, and not a setting. A
	// sift that takes something you wanted costs you the item; one that leaves
	// filler in the feed costs you a scroll, and those are not the same price.
	siftCut = 0.2
	// What an item no run has reached counts as in the best-first order:
	// neither good nor bad. Missing is not the same answer as low.
	siftUnknown = 0.5
	// How many items ride in one request. Jev reads the batch once and answers
	// every question against it, so bigger batches are cheaper per item — but
	// its context is 64k and its accuracy drifts as the state grows, and a batch
	// this size is a few thousand tokens with room to spare.
	siftBatch = 25
	// How many batches are in flight at once. The rate limits are far above
	// this; it is the feed's own courtesy, and it puts a few hundred items
	// through in under a minute.
	siftWorkers = 4
	// Per-item text budget. A judgment of whether something is worth reading is
	// made on the opening of it, the way yours is — and half the point of the
	// exercise is that a backlog's worth of bodies stays small.
	siftBodyRunes = 400
	// One request's patience, and the run's. A batch is a second or two; a
	// minute means something is wrong with the network, not with the batch.
	siftReqTimeout = 60 * time.Second
	siftRunTimeout = 30 * time.Minute
	// How many times a batch is retried when TypeSafe says it is busy (429) or
	// overloaded (529), and how long the first wait is. Anything else fails the
	// run: a bad key will not become a good one by being asked again.
	siftRetries = 3
	siftBackoff = 2 * time.Second
)

// siftReader is who the items are being judged for, in the model's state. It is
// a constant rather than a setting: the question a sift answers is "is there
// anything in this", which is the same question for everybody reading a feed
// they chose the sources of themselves.
const siftReader = `Someone working through their own feed backlog: sources they picked ` +
	`themselves, read for what is in the items rather than for company. They want ` +
	`what carries information, an argument, a result, a piece of news, a thing made ` +
	`or a thing explained. They do not want filler, and there is a lot of it.`

const siftQuestion = `Is this item worth the reader's time to open and read?`

const siftRankQuestion = `How much does this item repay opening?`

// siftLevel is one rung of the ladder the sift puts an item on: the key a chip
// is picked by, the word that chip wears, and the situation the model is given
// to recognize. Three rungs, because the row has to fit on a phone beside the
// sources and the content types, and because the decision a backlog actually
// asks of you has three answers: glance, open it, open it now.
//
// The order is the ladder's: lowest first, which is the order the API wants its
// levels in and the index a stored rank is (plus one, so an unjudged item's
// zero is not a rung — see feedEntry.Rank).
type siftLevel struct {
	Key   string
	Label string
	Says  string
}

var siftLevels = []siftLevel{
	{
		Key: "skim", Label: "skim",
		Says: "A glance is the whole of it: a link with a line around it, a short " +
			"update, a note you would take in from the card without opening anything.",
	},
	{
		Key: "click", Label: "worth a click",
		Says: "There is a real thing in it and the item is not the thing: an argument " +
			"to follow, a result, a piece of news to act on, something made or explained. " +
			"Opening it pays for the minute.",
	},
	{
		Key: "must", Label: "must read",
		Says: "The one you would be annoyed to have missed. A result or a decision that " +
			"changes what the reader does, or an explanation good enough to change how " +
			"they think about something they already care about. Rare — most days have " +
			"a handful at most.",
	},
}

// siftRankOf turns the model's probability-weighted score into a rung: 1 to
// len(siftLevels), with 0 kept for an item nothing has judged. Rounded rather
// than taken from the likeliest level, since a score that lands between two
// rungs is a genuine halfway and the round is the honest reading of it.
func siftRankOf(score float64) int {
	r := int(math.Round(score)) + 1
	if r < 1 {
		return 1
	}
	if r > len(siftLevels) {
		return len(siftLevels)
	}
	return r
}

// siftLevelOf is the rung a rank names, and whether it names one at all.
func siftLevelOf(rank int) (siftLevel, bool) {
	if rank < 1 || rank > len(siftLevels) {
		return siftLevel{}, false
	}
	return siftLevels[rank-1], true
}

// siftJob is one run, and the whole of what the page's button needs to draw
// itself: the state machine, how far through it is, and what it came to.
type siftJob struct {
	State    string `json:"state"` // "running", "done" or "failed"
	Done     int    `json:"done"`  // items judged so far
	Total    int    `json:"total"` // ...of this many
	Aside    int    `json:"aside"` // of those, set aside
	Err      string `json:"error,omitempty"`
	Finished string `json:"finished,omitempty"`
}

// sifter runs the sift. One run at a time, on the server's own clock rather
// than inside a request: a few hundred items is a minute of round trips, and
// the tap that asked for it is long gone by then.
//
// judge is the call to the model, swapped out in tests, which have no business
// spending an API key to find out whether a handler validates its form.
type sifter struct {
	judge func(ctx context.Context, items []core.Item) (map[string]siftVerdict, error)
	cache *feedCache
	queue chan struct{}
	mu    sync.Mutex
	job   siftJob
}

func newSifter(cache *feedCache) *sifter {
	return &sifter{judge: typesafeJudge, cache: cache, queue: make(chan struct{}, 1)}
}

// serve is the one worker, taking a run at a time until the server stops.
func (s *sifter) serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.queue:
			s.run(ctx)
		}
	}
}

// start queues a run unless one is already going. The running state is written
// before the queueing, so a button that has just been tapped reads as busy.
func (s *sifter) start() error {
	if strings.TrimSpace(os.Getenv(typesafeKey)) == "" {
		return errors.New(typesafeKey + " is not set: put a TypeSafe API key in the env file to sift")
	}
	total := s.cache.unjudgedCount()
	if total == 0 {
		return errors.New("every unread item has been judged already")
	}
	s.mu.Lock()
	if s.job.State == "running" {
		s.mu.Unlock()
		return nil // already going: the tap that started it says the same thing
	}
	s.job = siftJob{State: "running", Total: total}
	s.mu.Unlock()
	select {
	case s.queue <- struct{}{}:
		return nil
	default:
		s.mu.Lock()
		s.job = siftJob{}
		s.mu.Unlock()
		return errors.New("a sift is already waiting to start")
	}
}

func (s *sifter) state() siftJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.job
}

// run puts the whole unjudged backlog through the model, a batch at a time,
// several batches at once. Each batch is recorded as it lands rather than at
// the end: a run that dies halfway has still done the half it did, and tapping
// sift again picks up exactly where it stopped.
//
// The first failure stops the rest. A key that is wrong, a plan that is out of
// credit or a service that is down are all the same answer for every remaining
// batch, and spending forty more requests to hear it forty more times helps
// nobody.
func (s *sifter) run(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, siftRunTimeout)
	defer cancel()

	items := s.cache.unjudged(time.Now())
	batches := make(chan []core.Item)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for i := 0; i < siftWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range batches {
				verdicts, err := s.judge(ctx, batch)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
						cancel() // the rest would only fail the same way
					}
					mu.Unlock()
					return
				}
				s.landed(verdicts)
			}
		}()
	}
feeding:
	for start := 0; start < len(items); start += siftBatch {
		end := start + siftBatch
		if end > len(items) {
			end = len(items)
		}
		select {
		case batches <- items[start:end]:
		case <-ctx.Done():
			break feeding
		}
	}
	close(batches)
	wg.Wait()

	// Whatever landed is worth keeping, failure or not.
	if err := s.cache.save(); err != nil && firstErr == nil {
		firstErr = err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job.Finished = time.Now().UTC().Format(time.RFC3339)
	switch {
	case firstErr != nil:
		s.job.State, s.job.Err = "failed", firstErr.Error()
	case parent.Err() != nil:
		s.job.State, s.job.Err = "failed", "the server stopped before the sift finished"
	default:
		s.job.State = "done"
	}
}

func (s *sifter) landed(verdicts map[string]siftVerdict) {
	aside := s.cache.judge(verdicts, time.Now())
	s.mu.Lock()
	s.job.Done += len(verdicts)
	s.job.Aside += aside
	s.mu.Unlock()
}

// siftVerdict is what one run made of one item: whether there is anything in it
// at all, which is what the cut is drawn on, and how far it repays opening,
// which is what the chips group by and the best-first order runs on. Two
// questions rather than one because they are different questions — a yes/no
// answers "is this junk", and only a ladder can tell a solid post from the one
// you would have been sorry to miss.
type siftVerdict struct {
	Worth float64
	Rank  int
}

// typesafeJudge asks Jev about one batch and returns what it made of each item,
// by feed key. An item the answer says nothing about is left out rather than
// guessed at: unjudged is a state this whole thing is built to survive, and a
// made-up number is not.
func typesafeJudge(ctx context.Context, items []core.Item) (map[string]siftVerdict, error) {
	key := strings.TrimSpace(os.Getenv(typesafeKey))
	if key == "" {
		return nil, errors.New(typesafeKey + " is not set")
	}
	body, err := json.Marshal(siftRequest(items))
	if err != nil {
		return nil, err
	}

	var answer struct {
		Answers map[string]struct {
			Noul  float64 `json:"noul"`
			Score float64 `json:"score"`
		} `json:"answers"`
	}
	wait := siftBackoff
	for attempt := 0; ; attempt++ {
		raw, status, err := postTypesafe(ctx, key, body)
		if err == nil {
			if err := json.Unmarshal(raw, &answer); err != nil {
				return nil, fmt.Errorf("typesafe returned something that is not an answer: %w", err)
			}
			break
		}
		// Busy and overloaded are the two the docs ask us to come back from.
		if attempt >= siftRetries || (status != http.StatusTooManyRequests && status != 529) {
			return nil, err
		}
		select {
		case <-time.After(wait):
			wait *= 2
		case <-ctx.Done():
			return nil, err
		}
	}

	out := make(map[string]siftVerdict, len(items))
	for i, it := range items {
		worth, ok := answer.Answers[siftAskID(i)]
		if !ok {
			continue
		}
		// A batch that came back with the cut but not the ladder is half an
		// answer, and half an answer is not a judgment: leave the item for the
		// next run rather than filing it on a rung nobody picked.
		rank, ok := answer.Answers[siftRankID(i)]
		if !ok {
			continue
		}
		out[core.Key(it.App, it.ID)] = siftVerdict{Worth: worth.Noul, Rank: siftRankOf(rank.Score)}
	}
	if len(out) == 0 {
		return nil, errors.New("typesafe answered none of the batch")
	}
	return out, nil
}

func postTypesafe(ctx context.Context, key string, body []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, siftReqTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, typesafeURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("typesafe: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("typesafe: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("typesafe said %s: %s", resp.Status, typesafeTrouble(raw))
	}
	return raw, resp.StatusCode, nil
}

// typesafeTrouble pulls the readable part out of an error body. The API
// describes what it refused in JSON; a toast has room for the sentence, not the
// structure.
func typesafeTrouble(raw []byte) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	_ = json.Unmarshal(raw, &body)
	for _, s := range []string{body.Error.Message, body.Message, body.Detail, string(raw)} {
		if s = strings.TrimSpace(oneLine(s)); s != "" {
			return clipRunes(s, 200)
		}
	}
	return "no reason given"
}

func siftAskID(i int) string  { return "w" + strconv.Itoa(i) }
func siftRankID(i int) string { return "r" + strconv.Itoa(i) }

type siftState struct {
	Reader string      `json:"reader"`
	Items  []siftEntry `json:"items"`
}

// siftEntry is one item as the model sees it: what it is, where it came from
// and how it opens. No URL and no id — neither is anything to judge, and both
// are tokens spent on every item in the batch.
type siftEntry struct {
	N      int    `json:"n"`
	Source string `json:"source,omitempty"`
	Author string `json:"author,omitempty"`
	Age    string `json:"age,omitempty"`
	Kind   string `json:"carries,omitempty"`
	Title  string `json:"title,omitempty"`
	Text   string `json:"text,omitempty"`
}

// siftAsk is one question. Criteria is what a yes and a no mean for the cut and
// the ordered rungs for the ladder, which is why it is left to the two builders
// below rather than typed here.
type siftAsk struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria"`
}

type siftBody struct {
	Model     string             `json:"model"`
	State     siftState          `json:"state"`
	Questions map[string]siftAsk `json:"questions"`
}

// siftRequest builds one batch: the items as state, and two questions per item
// pointing at its place in that state — the yes/no the cut is drawn on and the
// ladder the chips group by. Every question in the batch is asked together
// rather than one request each because Jev reads the state once either way, so
// the second question costs a fraction of the first item it is asked about —
// which is the whole reason a backlog this deep is affordable at all.
func siftRequest(items []core.Item) siftBody {
	b := siftBody{
		Model:     typesafeModel,
		State:     siftState{Reader: siftReader, Items: make([]siftEntry, 0, len(items))},
		Questions: make(map[string]siftAsk, len(items)),
	}
	for i, it := range items {
		e := siftEntry{N: i, Source: strings.TrimSpace(it.Source), Age: it.Age}
		if a := strings.TrimSpace(it.Author); a != "" && a != e.Source {
			e.Author = a
		}
		if ty := itemType(it); ty != "text" {
			e.Kind = ty
		}
		title := strings.TrimSpace(itemTitle(it))
		body := strings.TrimSpace(it.Body)
		// An x post is all body, and one whose title is that body over again
		// would be sent twice.
		if title != "" && title != body {
			e.Title = oneLine(title)
		}
		if body != "" {
			e.Text = clipRunes(body, siftBodyRunes)
		}
		b.State.Items = append(b.State.Items, e)
		b.Questions[siftAskID(i)] = siftAsk{
			Type:         "noul",
			Instructions: fmt.Sprintf("%s The item is `items[%d]`.", siftQuestion, i),
			Criteria: map[string]string{
				"true": "It carries something: information, an argument, a result, news, a " +
					"thing made or a thing explained. Worth the minute even if it is short.",
				"false": "Filler: a greeting or a mood, engagement bait, a meme, an ad or a " +
					"promotion, a giveaway, a repost of a thing everyone has seen, small talk, " +
					"or a headline with nothing behind it.",
			},
		}
		b.Questions[siftRankID(i)] = siftAsk{
			Type:         "score",
			Instructions: fmt.Sprintf("%s The item is `items[%d]`.", siftRankQuestion, i),
			Criteria:     siftRungs(),
		}
	}
	return b
}

// siftRungs is the ladder as the API wants it: the level descriptions in order,
// lowest first.
func siftRungs() []string {
	out := make([]string, 0, len(siftLevels))
	for _, l := range siftLevels {
		out = append(out, l.Says)
	}
	return out
}

// startSift (POST /sift) puts the unjudged backlog through the model and
// answers at once with the job, not the result: the wait is a minute, and the
// page carries on being read while it runs.
func startSift(w http.ResponseWriter, sift *sifter) {
	if err := sift.start(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeSiftJSON(w, http.StatusAccepted, sift.state())
}

// showSift (GET /sift) reports on the run, which is what the button polls while
// one is going. left is what a run started now would have to get through, which
// is what the button says when nothing is running.
func showSift(w http.ResponseWriter, sift *sifter) {
	job := sift.state()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		siftJob
		Left    int `json:"left"`
		Skipped int `json:"skipped"`
	}{job, sift.cache.unjudgedCount(), sift.cache.skippedCount()})
}

func writeSiftJSON(w http.ResponseWriter, code int, job siftJob) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(job)
}
