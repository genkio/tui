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
//
// A sift follows every fetch rather than a tap (see sifter.auto): judging what
// a sweep brought in is the same work whenever it is done, and done then it is
// done before the feed is looked at. The button is still there, for the times
// something else has left the backlog unjudged — an edited interest list, a run
// that failed on a key — but a feed nobody touches is sifted anyway.
const (
	typesafeURL   = "https://api.typesafe.ai/v1/systemone"
	typesafeModel = "jev-latest"
	typesafeKey   = "TYPESAFE_API_KEY"
	// Where the line falls: an item is set aside when the model puts its chance
	// of being worth reading below this. Deliberately low, and not a setting. A
	// sift that takes something you wanted costs you the item; one that leaves
	// filler in the feed costs you a scroll, and those are not the same price.
	siftCut = 0.2
	// Where a match is called a match: how sure the pick has to be before an
	// item is filed under a subject. High, because the alternative is already
	// on the table — the question offers "none of them" as an option, and an
	// item that is about one of your subjects picks it at 0.95 and up. What
	// lands between the two is the genuinely unclear, and the chip is better
	// without it.
	interestCut = 0.8
	// What an item no run has reached counts as in the best-first order:
	// neither good nor bad. Missing is not the same answer as low.
	siftUnknown = 0.5
	// How many items are in the air at once. One request an item means a
	// thousand of them in a run, so this is what decides whether that is a
	// minute or twenty — and it is bounded by TypeSafe's 1,200 requests a
	// minute against a round trip of about half a second, not by anything here.
	siftWorkers = 8
	// Per-item text budget. A judgment of whether something is worth reading is
	// made on the opening of it, the way yours is.
	siftBodyRunes = 400
	// One request's patience, and the run's. A batch is a second or two; a
	// minute means something is wrong with the network, not with the batch.
	siftReqTimeout = 60 * time.Second
	siftRunTimeout = 30 * time.Minute
	// How many times a batch is retried when TypeSafe says it is busy (429) or
	// overloaded (529), and how long the first wait is. Anything else fails the
	// run: a bad key will not become a good one by being asked again.
	siftRetries = 4
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

// The list as one question with an escape hatch, rather than a yes/no per
// subject. Both were tried on a real backlog and the difference is not subtle.
// Asked as yes/no, "is this about Japan permanent residency?" has nothing to
// weigh against, and a Cantonese post about robotaxis in Singapore comes back
// at 0.81 — as high as the immigration paperwork it is supposed to be finding.
// Asked as a choice against an explicit "none of them", the same post is none
// at 1.00 and the paperwork is the subject at 0.95. The escape hatch is the
// whole difference: a probability only means something against the
// alternatives, and "none" is the alternative that is true nearly every time.
//
// And it is asked of one item at a time, alone, which is the other half of the
// same lesson. Whether something is about a subject you named is a rare-event
// question, and a rare-event question is not safe in company: in a batch of
// twenty-five, that Waymo post — sitting among other Cantonese posts, none of
// them about immigration — came back as Japan permanent residency at 0.90,
// while the actual residency thread in another batch came back as none. Asked
// on its own each one is right, at 1.00 and 0.98. The cut and the ladder are
// comparative judgments and travel in batches happily; this one does not.
const siftInterestQuestion = `Which of the reader's subjects is this item about?`

// siftNoSubject is that escape hatch, and its description says what the answer
// usually is — the model is being asked about a backlog, not a shortlist.
const siftNoSubject = "none"

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

// errNoKey is a sift that cannot happen at all, told apart from the ordinary
// refusals because a fetch asks for a sift every time now: the reason the feed
// is never sifted is worth hearing once, and hearing once is enough.
var errNoKey = errors.New(typesafeKey + " is not set: put a TypeSafe API key in the env file to sift")

// sifter runs the sift. One run at a time, on the server's own clock rather
// than inside a request: a few hundred items is a minute of round trips, and
// the tap that asked for it is long gone by then.
//
// judge is the call to the model, swapped out in tests, which have no business
// spending an API key to find out whether a handler validates its form.
type sifter struct {
	judge func(ctx context.Context, it core.Item, interests []string) (siftVerdict, error)
	cache *feedCache
	// What the reader is following at the moment, read once when a run starts:
	// the list is the same for every batch in that run, and one edited halfway
	// through would leave a backlog judged against two different lists.
	interests *interestStore
	queue     chan struct{}
	said      sync.Once
	// Where a run that failed says so, for the banner: nearly every sift is one
	// a fetch asked for, with no button being watched and nobody to toast.
	mishaps *mishapLog
	mu      sync.Mutex
	job     siftJob
}

func newSifter(cache *feedCache, interests *interestStore, mishaps *mishapLog) *sifter {
	return &sifter{
		judge: typesafeJudge, cache: cache, interests: interests,
		mishaps: mishaps, queue: make(chan struct{}, 1),
	}
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

// auto is the sift a fetch asks for, which is nearly every sift there is: the
// sweeper calls it at the end of a sweep, so what a fetch brought in is judged
// before anyone looks at it. A backlog that is already judged, or a run still
// going, is the ordinary answer here and not worth a word — the sweeper asks
// every quarter of an hour whether there is anything to do.
func (s *sifter) auto() {
	if err := s.start(); errors.Is(err, errNoKey) {
		s.said.Do(func() { logf("sift: %v", err) })
	}
}

// start queues a run unless one is already going. The running state is written
// before the queueing, so a button that has just been tapped reads as busy.
func (s *sifter) start() error {
	if strings.TrimSpace(os.Getenv(typesafeKey)) == "" {
		return errNoKey
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

	var interests []string
	if s.interests != nil {
		interests = s.interests.list()
	}
	queue := make(chan core.Item)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for i := 0; i < siftWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range queue {
				v, err := s.judge(ctx, it, interests)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
						cancel() // the rest would only fail the same way
					}
					mu.Unlock()
					return
				}
				s.landed(core.Key(it.App, it.ID), v)
			}
		}()
	}
feeding:
	for _, it := range s.cache.unjudged(time.Now()) {
		select {
		case queue <- it:
		case <-ctx.Done():
			break feeding
		}
	}
	close(queue)
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
		// ...and on the banner, where a fetch's own sift has its only reader. A
		// shutdown below is nobody's trouble and says nothing.
		s.mishaps.note("sift", s.job.Err)
	case parent.Err() != nil:
		s.job.State, s.job.Err = "failed", "the server stopped before the sift finished"
	default:
		s.job.State = "done"
	}
}

func (s *sifter) landed(key string, v siftVerdict) {
	aside := s.cache.judge(map[string]siftVerdict{key: v}, time.Now())
	s.mu.Lock()
	s.job.Done++
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
	// How well it answers the reader's own list, or below zero when there was
	// no list to answer — which is not the same as being asked and missing it.
	// InterestFor is the subject that answered best, which is what the card
	// wears: a chip you cannot ask "why is this here" of is one you end up
	// ignoring.
	Interest    float64
	InterestFor string
}

// typesafeJudge asks Jev about one item: the cut, the ladder, and — when the
// reader has a list — which of their subjects it is about. One item to a
// request, which is the whole lesson of this file. Every one of the three
// questions was tried in batches of twenty-five first, and every one of them
// came back differently depending on the company the item kept: a two-word
// "牛逼啊！" was worth 0.57 among its neighbours and 0.04 on its own, a
// sideloading tutorial 0.30 among them and 0.85 alone. The state is read once
// per request either way, so a batch was never the saving it looked like: it
// shared the reading of twenty-five items rather than the reading of one, and
// paid for it in every answer.
func typesafeJudge(ctx context.Context, it core.Item, interests []string) (siftVerdict, error) {
	v := siftVerdict{Interest: -1}
	key := strings.TrimSpace(os.Getenv(typesafeKey))
	if key == "" {
		return v, errors.New(typesafeKey + " is not set")
	}
	body, err := json.Marshal(siftRequest(it, interests))
	if err != nil {
		return v, err
	}
	raw, err := askTypesafe(ctx, key, body)
	if err != nil {
		return v, err
	}
	var answer struct {
		Answers map[string]struct {
			Noul          float64            `json:"noul"`
			Score         float64            `json:"score"`
			Choice        string             `json:"choice"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return v, fmt.Errorf("typesafe returned something that is not an answer: %w", err)
	}
	worth, ok := answer.Answers[siftAskID]
	if !ok {
		return v, errors.New("typesafe did not say whether that is worth reading")
	}
	// Half an answer is not a judgment: leave the item for the next run rather
	// than filing it on a rung nobody picked.
	rank, ok := answer.Answers[siftRankID]
	if !ok {
		return v, errors.New("typesafe did not say how much that repays opening")
	}
	v.Worth, v.Rank = worth.Noul, siftRankOf(rank.Score)
	// No list means no question, which leaves the item unasked rather than
	// asked and unmatched — and those are different states (see feedEntry).
	if match, ok := answer.Answers[siftInterestID]; ok {
		v.Interest, v.InterestFor = siftMatch(match.Choice, match.Probabilities, interests)
	}
	return v, nil
}

// siftRetryable reports whether an answer is worth asking again. Busy (429) and
// overloaded (529) are the two the docs name, and everything else in the 500s
// joins them by observation: a run of a thousand items is a thousand round
// trips, and somewhere in one of them their edge will answer "upstream connect
// error" — a sentence about their afternoon, not about the request.
func siftRetryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// askTypesafe posts one request and hands back its body, coming back from the
// answers worth coming back from and from nothing else: a key that is wrong
// will not become right by being asked again.
func askTypesafe(ctx context.Context, key string, body []byte) ([]byte, error) {
	wait := siftBackoff
	for attempt := 0; ; attempt++ {
		raw, status, err := postTypesafe(ctx, key, body)
		if err == nil {
			return raw, nil
		}
		if attempt >= siftRetries || !siftRetryable(status) {
			return nil, err
		}
		select {
		case <-time.After(wait):
			wait *= 2
		case <-ctx.Done():
			return nil, err
		}
	}
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

// What the three questions are called. Fixed rather than numbered: a request
// carries one item, so there is one of each.
const (
	siftAskID      = "worth"
	siftRankID     = "rung"
	siftInterestID = "subject"
)

// siftSubjectID is what one subject is called in the question: its place on the
// list rather than its text, so a subject written as "none" or as an empty line
// cannot collide with the escape hatch or with another one.
func siftSubjectID(j int) string { return "s" + strconv.Itoa(j) }

// siftSubjects is the list as the options of a choice: one per subject, with
// what the reader typed as its description, and the escape hatch last.
func siftSubjects(interests []string) map[string]string {
	out := make(map[string]string, len(interests)+1)
	for j, subject := range interests {
		out[siftSubjectID(j)] = subject
	}
	out[siftNoSubject] = "None of them. This is the ordinary answer: a backlog is mostly " +
		"other things, and an item that is merely adjacent to a subject belongs here."
	return out
}

// siftMatch reads the answer back: which subject was picked, and how sure of it
// the model was. Nothing picked, or the escape hatch picked, is no match — and
// so is a pick the answer is not sure enough of (see interestCut).
func siftMatch(choice string, probabilities map[string]float64, interests []string) (float64, string) {
	if choice == "" || choice == siftNoSubject {
		return 0, ""
	}
	for j, subject := range interests {
		if siftSubjectID(j) != choice {
			continue
		}
		return probabilities[choice], subject
	}
	return 0, ""
}

// siftState is what one request carries: who is reading, and the one item being
// judged. One item, because every judgment here came back sharper alone than in
// company — see typesafeJudge.
type siftState struct {
	Reader string    `json:"reader"`
	Item   siftEntry `json:"item"`
}

// siftEntry is one item as the model sees it: what it is, where it came from
// and how it opens. No URL and no id — neither is anything to judge, and both
// are tokens spent on every item in the batch.
type siftEntry struct {
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
	State     any                `json:"state"`
	Questions map[string]siftAsk `json:"questions"`
}

// siftRequest is one item's request: the item alone as the state, and the three
// questions asked of it. The subjects ride as the options of the choice rather
// than as state — the question is which of them this is about, and the options
// are the only place that needs to say.
func siftRequest(it core.Item, interests []string) siftBody {
	b := siftBody{
		Model: typesafeModel,
		State: siftState{Reader: siftReader, Item: siftEntryOf(it)},
		Questions: map[string]siftAsk{
			siftAskID: {
				Type:         "noul",
				Instructions: siftQuestion,
				Criteria: map[string]string{
					"true": "It carries something: information, an argument, a result, news, a " +
						"thing made or a thing explained. Worth the minute even if it is short.",
					"false": "Filler: a greeting or a mood, engagement bait, a meme, an ad or a " +
						"promotion, a giveaway, a repost of a thing everyone has seen, small talk, " +
						"or a headline with nothing behind it.",
				},
			},
			siftRankID: {
				Type:         "score",
				Instructions: siftRankQuestion,
				Criteria:     siftRungs(),
			},
		},
	}
	// Only when there is a list. An empty one would be a question whose only
	// possible answer is "none", asked of every item in the backlog.
	if len(interests) > 0 {
		b.Questions[siftInterestID] = siftAsk{
			Type: "choice",
			Instructions: siftInterestQuestion + " Pick a subject only if the item is plainly " +
				"about it — not the field it sits in, not the country it happens in, not " +
				"something that would merely interest the same person.",
			Criteria: siftSubjects(interests),
		}
	}
	return b
}

// siftEntryOf is one item as the model sees it, wherever it is going: in a
// batch beside two dozen others, or alone as the whole state of a request.
func siftEntryOf(it core.Item) siftEntry {
	e := siftEntry{Source: strings.TrimSpace(it.Source), Age: it.Age}
	if a := strings.TrimSpace(it.Author); a != "" && a != e.Source {
		e.Author = a
	}
	if ty := itemType(it); ty != "text" {
		e.Kind = ty
	}
	title := strings.TrimSpace(itemTitle(it))
	body := strings.TrimSpace(it.Body)
	// An x post is all body, and one whose title is that body over again would
	// be sent twice.
	if title != "" && title != body {
		e.Title = oneLine(title)
	}
	if body != "" {
		e.Text = clipRunes(body, siftBodyRunes)
	}
	return e
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
