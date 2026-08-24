package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/genkio/tui/core"
)

// The page markup/CSS/JS lives in page.tmpl, embedded so the release stays a
// single self-contained binary. --dev re-reads the file per request instead.
//
//go:embed page.tmpl
var pageSrc string

// pageLoader hands out the parsed page template: the embedded copy parsed once
// for normal runs, or a fresh re-read of devPath on every call under --dev so
// template edits show up on browser refresh without a rebuild.
type pageLoader struct {
	devPath string
	tmpl    *template.Template
}

func newPageLoader(devPath string) (*pageLoader, error) {
	if devPath != "" {
		return &pageLoader{devPath: devPath}, nil
	}
	t, err := template.New("page").Parse(pageSrc)
	if err != nil {
		return nil, fmt.Errorf("embedded page.tmpl: %w", err)
	}
	return &pageLoader{tmpl: t}, nil
}

func (l *pageLoader) load() (*template.Template, error) {
	if l.devPath == "" {
		return l.tmpl, nil
	}
	src, err := os.ReadFile(l.devPath)
	if err != nil {
		return nil, err
	}
	return template.New("page").Parse(string(src))
}

// feedSel is the chip that is on. At most one is, because a pick is a page load
// of that chip's items and nothing else: Kind is "app", "type", "x" (x's For
// You, which is fetched live rather than read from the backlog), or "" for the
// whole list.
type feedSel struct {
	Kind string
	Key  string
}

func (s feedSel) on() bool { return s.Kind != "" }

// String names the pick for the page's own script, which needs to know only
// whether one is on and which chip it was.
func (s feedSel) String() string {
	if !s.on() {
		return ""
	}
	return s.Kind + ":" + s.Key
}

// feedTally counts a list along the axes the chips slice it by. Taken before a
// pick narrowed the page, so every chip still states what picking it would
// bring rather than what is left after the current one.
type feedTally struct {
	apps  map[string]int
	types map[string]int
}

func newTally() feedTally {
	return feedTally{apps: map[string]int{}, types: map[string]int{}}
}

// tallyItems counts a whole list; the feed hands this in from before it applied
// the pick.
func tallyItems(items []core.Item) feedTally {
	t := newTally()
	for _, it := range items {
		t.apps[it.App]++
		t.types[itemType(it)]++
	}
	return t
}

// unread is the whole list's size, which is what the header counts: the chips
// slice it up, but that number is every source's backlog whichever chip is on.
func (t feedTally) unread() int {
	n := 0
	for _, c := range t.apps {
		n += c
	}
	return n
}

func tallyCards(cards []cardData) feedTally {
	t := newTally()
	for _, c := range cards {
		t.apps[c.App]++
		t.types[c.Type]++
	}
	return t
}

// pageInput is everything a render needs; a struct because the feed view and
// the saved view fill in different halves of it.
type pageInput struct {
	items     []core.Item
	total     int // everything the pick matched, of which items is at most a window
	apps      []string
	failed    []string
	now       time.Time
	sel       feedSel
	tally     *feedTally // the chip counts from before sel narrowed items; nil when it didn't
	query     url.Values // this page's query, which the chip links are built from
	warn      string
	saved     *savedStore
	savedView bool
	// The block list, on every view: the header counts it from the feed and the
	// saved list, and the blocked view renders it.
	block       *blocker
	blockedView bool
	swipe       bool // --swipe: one card at a time instead of the scrolling feed
	asc         bool // oldest first, which the header's toggle flips
	updated     time.Time
	fetching    bool // a sweep is in flight, so the count is about to move
	capped      bool // a service's backlog runs deeper than the sweep reached
}

type pageData struct {
	// Every source's unread, whichever chip is on: the chips already say what
	// each of them holds, so the header saying the same thing over again would
	// leave nothing stating the whole. JS decrements it in place.
	Unread    int
	Shown     int  // how much of the pick this page carries
	More      bool // ...and there is more of it behind, so reload after clearing
	Behind    int  // how much more, stated on the button so the two numbers add up
	Capped    bool // even the backlog is short of the truth; render it as "N+"
	Updated   string
	Fetching  bool
	Saved     int  // size of the saved list, in the header and its link
	SavedView bool // rendering the saved list rather than the live feed
	// The block list. Blocked is its size, in the header and its link on every
	// view; Keywords is how many words fill it, and KeywordText is those words
	// as the modal's textarea shows them — one per line, blocked view only.
	Blocked     int
	BlockedView bool
	Keywords    int
	KeywordText string
	Swipe       bool // deck of one card at a time, swiped through
	// Which way the feed runs, and the page that turns it around. SortHref is
	// blank on the views the toggle has no say over (saved, blocked), which is
	// how the template knows not to draw it.
	Asc       bool
	SortHref  string
	Warn      string
	HasApps   bool
	Sel       string // the chip that is on ("app:x"), blank for the whole list
	ClearHref string // ...and where to go to put it back, blank when none is on
	Filters   []filterGroup
	Cards     []cardData
}

// filterGroup is one axis a list can be narrowed along. Its chips are all
// alternatives now: picking one is a page of that chip's items, so the groups
// are only a matter of where the row breaks.
type filterGroup struct {
	Chips []filterChip
}

type filterChip struct {
	Kind  string // "app", "type" or "x", the axis this chip picks along
	Key   string
	Label string
	Color string // the source's own chip color; blank for the content types
	Count int
	Href  string // the page this chip loads; the one already on links back to the whole list
	On    bool
	// x's For You is scraped on the spot and never cached, so from anywhere else
	// there is no backlog of it to count: the chip is the icon alone until a
	// round of it has actually been fetched.
	Uncounted bool
	// A source chip is also that service's status light: its count is drawn
	// green when the last sweep worked and red when it didn't, which is the job
	// the header's separate row of dots used to do. Empty for a chip that isn't
	// a service (the content types, and the saved list, which is read off disk).
	State string // "ok", "bad", or ""
	Title string // what the state means, for a hover or long press
}

type cardData struct {
	App, ID     string
	Chip, Color string
	Source      string
	Author      string // blank when it duplicates Source
	Age         string
	Title       string // blank when x carries the full text as both title and body
	PreviewBody template.HTML
	FullBody    template.HTML
	URL         string
	Video       string // mp4 for the inline player: the app's own, or this server's bilibili route
	Keep        string // where the footer's keep link saves that mp4 from
	Poster      string
	VidLen      string // "1:23" badge over the poster; blank when the length is unknown
	HasVideo    bool   // this card or its quote has a player, so show the shared controls
	Audio       string // attached episode file; the card shows an inline audio player
	RedGif      string // redgifs clip id; the footer offers to fetch and play it
	Type        string // what the card carries: "video", "audio" or "text"
	Images      []string
	HasImage    bool // this card or its quote has stills, so offer the image toggle
	Quote       *quoteData
	Expand      bool
	Saved       bool    // starred: the footer button offers to unsave it
	Pos         float64 // seconds into PosSrc to resume at; 0 when there is nothing to resume
	PosSrc      string  // the stream that position belongs to
	// A blocked row: the title and where it came from, without the content that
	// is the part you asked not to see, and the keyword that caught it.
	Compact bool
	Keyword string
}

// cardImages prepares an app's stills for the card, sending the ones the
// browser cannot fetch itself through this server instead.
func cardImages(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		out = append(out, proxied(u))
	}
	return out
}

// proxied routes an image via /img when its host will not serve the page
// directly. doubanio wants a Referer naming a douban page, which no page here
// can produce, so the server fetches those on the browser's behalf. Everything
// else is loaded straight from its source.
func proxied(u string) string {
	if !strings.Contains(u, ".doubanio.com/") {
		return u
	}
	return "/img?u=" + url.QueryEscape(u)
}

// quoteData is the embedded post drawn inside a card: x's quote box.
type quoteData struct {
	Source      string
	Author      string // blank when it duplicates Source
	PreviewBody template.HTML
	FullBody    template.HTML
	URL         string
	Video       string
	Poster      string
	VidLen      string
	Images      []string
}

// buildPageData shapes the items into what page.tmpl renders: header counts,
// per-service health dots, and one card per item.
func buildPageData(in pageInput) pageData {
	xAuth := false
	for _, a := range in.apps {
		if a == "x" {
			xAuth = true
			break
		}
	}

	bad := map[string]bool{}
	for _, f := range in.failed {
		bad[f] = true
	}

	// The saved and blocked lists are for looking back over, not triage: no deck
	// on either, whatever the server was started with.
	swipe := in.swipe && !in.savedView && !in.blockedView
	cl := listClips
	if swipe {
		cl = swipeClips
	}
	cards := make([]cardData, 0, len(in.items))
	for _, it := range in.items {
		if in.blockedView {
			cards = append(cards, buildBlockedCard(it, in.block.caughtBy(it.App, it.ID)))
			continue
		}
		// In the saved view every card is saved by definition; in the feed ask
		// the store.
		starred := in.savedView || (in.saved != nil && in.saved.has(it.App, it.ID))
		card := buildCard(it, starred, cl)
		// Where the item was left off, kept with the item itself. The feed shows
		// it too: a card starred earlier resumes wherever you got to in it.
		if in.saved != nil {
			card.Pos, card.PosSrc = in.saved.pos(it.App, it.ID)
		}
		cards = append(cards, card)
	}

	savedCount := 0
	if in.saved != nil {
		savedCount = in.saved.count()
	}
	keywordText := ""
	if in.blockedView {
		keywordText = in.block.keywordText()
	}

	// A pick is a page load of one chip's items, so the counts have to come from
	// before the narrowing — hence the caller's tally, and the cards only when
	// nothing narrowed them. On the feed the source chips are also the
	// per-service status, so they are drawn for every logged-in app whether or
	// not it has anything on this page.
	tally := tallyCards(cards)
	if in.tally != nil {
		tally = *in.tally
	}
	filters := chipRow(tally, in.apps, bad, xAuth, in.sel, in.query, in.total)
	clear := ""
	if in.sel.on() {
		clear = chipHref(in.query, feedSel{})
	}

	updated := ""
	if !in.updated.IsZero() {
		updated = humanAgo(in.updated)
	}

	// The saved and blocked lists are ordered by when you saved or blocked
	// something, which is the only order they are any use in, so the toggle is
	// left off those views rather than drawn there doing nothing.
	flip := ""
	if !in.savedView && !in.blockedView && len(in.apps) > 0 {
		flip = orderHref(in.query, in.asc)
	}

	return pageData{
		Unread:      tally.unread(),
		Shown:       len(in.items),
		More:        in.total > len(in.items),
		Behind:      max(0, in.total-len(in.items)),
		Capped:      in.capped,
		Updated:     updated,
		Fetching:    in.fetching,
		Saved:       savedCount,
		SavedView:   in.savedView,
		Blocked:     in.block.count(),
		BlockedView: in.blockedView,
		Keywords:    in.block.keywordCount(),
		KeywordText: keywordText,
		Filters:     filters,
		Sel:         in.sel.String(),
		ClearHref:   clear,
		Asc:         in.asc,
		SortHref:    flip,
		Swipe:       swipe,
		Warn:        in.warn,
		HasApps:     len(in.apps) > 0,
		Cards:       cards,
	}
}

// appLabel and appColor are how a service shows up on a card: its short chip
// name and the theme color the terminal gives it, with a fallback for an app
// the web view hasn't been taught yet.
func appLabel(app string) string {
	if l := appLabels[app]; l != "" {
		return l
	}
	return app
}

func appColor(app string) string {
	if c := appColors[app]; c != "" {
		return c
	}
	return "#4a9eff"
}

// itemTitle is the title a card shows: blank for a post that carries its whole
// text as both title and body (x), which has no headline of its own to draw
// above the text. It is also what the block list reads — one rule for what
// counts as a title, so the words match what you can see.
func itemTitle(it core.Item) string {
	title := strings.TrimSpace(it.Title)
	if body := strings.TrimSpace(it.Body); body != "" && body == title {
		return ""
	}
	return title
}

// buildBlockedCard is the compact row the blocked list renders: where it came
// from, its title, and the keyword that caught it. No body, no player, no
// stills — that is the part you asked not to see, and leaving it out is what
// keeps a long list scannable.
func buildBlockedCard(it core.Item, why string) cardData {
	author := it.Author
	if author == it.Source {
		author = ""
	}
	return cardData{
		App:     it.App,
		ID:      it.ID,
		Chip:    appLabel(it.App),
		Color:   appColor(it.App),
		Source:  it.Source,
		Author:  author,
		Age:     it.Age,
		Title:   itemTitle(it),
		URL:     it.URL,
		Type:    itemType(it),
		Keyword: why,
		Compact: true,
	}
}

func buildCard(it core.Item, starred bool, cl clips) cardData {
	chip, color := appLabel(it.App), appColor(it.App)

	title := itemTitle(it)
	body := strings.TrimSpace(it.Body)
	author := it.Author
	if author == it.Source {
		author = ""
	}

	c := cardData{
		App:    it.App,
		ID:     it.ID,
		Chip:   chip,
		Color:  color,
		Source: it.Source,
		Author: author,
		Age:    it.Age,
		Title:  title,
		URL:    it.URL,
		Video:  it.Video,
		Poster: it.Poster,
		VidLen: vidLen(it.VidSecs),
		Audio:  it.Audio,
		Images: cardImages(it.Images),
		Saved:  starred,
	}
	// Two content panels: a clipped preview and a full version the footer's
	// expand toggle reveals. linkify escapes, so the HTML is safe as-is.
	if body != "" {
		c.PreviewBody = template.HTML(preview(body, cl.body))
		c.FullBody = template.HTML(linkify(body))
	}
	if it.Quote != nil {
		c.Quote = buildQuote(*it.Quote, cl.quote)
	}
	if c.Video == "" {
		// A bilibili post links a watch page and nothing else: the stream behind it
		// is resolved and proxied by /bili, so the player points there. Everything
		// else that hides a clip behind a link is redgifs.
		if bv := biliVideoID(it.App, it.URL); bv != "" {
			c.Video = biliPath + "?id=" + bv
		} else {
			c.RedGif = redgifID(it.URL, it.Title, it.Body)
		}
	}
	c.Keep = keepURL(it.App, it.ID, c.Video)
	c.HasVideo = c.Video != "" || (c.Quote != nil && c.Quote.Video != "")
	c.HasImage = len(c.Images) > 0 || (c.Quote != nil && len(c.Quote.Images) > 0)
	c.Expand = needsExpand(body, title, cl.body) || (c.Quote != nil && c.Quote.PreviewBody != c.Quote.FullBody)
	c.Type = itemType(it)
	return c
}

// biliVideoRe matches the bvid in a bilibili watch URL. A series episode
// (/bangumi/play/ep…) has no bvid and so no stream this server can resolve; those
// cards stay a link out.
var biliVideoRe = regexp.MustCompile(`bilibili\.com/video/(BV[0-9A-Za-z]{10})`)

// biliVideoID is the bilibili video a card is about, blank when the item is not
// a bilibili post or does not name one.
func biliVideoID(app, rawURL string) string {
	if app != "bilibili" {
		return ""
	}
	if m := biliVideoRe.FindStringSubmatch(rawURL); m != nil {
		return m[1]
	}
	return ""
}

// keepURL is where the footer's keep link saves the card's clip from: a direct
// mp4 goes through /dl, which names the file and gets around the CDN turning away
// a cross-origin download, while a bilibili clip is already coming through this
// server and only needs to be asked for as an attachment.
func keepURL(app, id, video string) string {
	switch {
	case video == "":
		return ""
	case strings.HasPrefix(video, biliPath+"?"):
		return video + "&dl=1"
	default:
		return "/dl?n=" + app + "-" + id + ".mp4&u=" + url.QueryEscape(video)
	}
}

// ytLinkRe is the client's ytId() as a test: a card linking a YouTube video
// grows a player in the browser, so the saved list should call it a video even
// though nothing was attached to the item.
var ytLinkRe = regexp.MustCompile(`(?:youtube\.com/watch\?[^#]*v=|youtu\.be/|youtube\.com/shorts/|youtube\.com/embed/)[\w-]{11}`)

// itemType sorts an item by what it carries, so a list can be sliced into
// things to watch, things to listen to, and things to read. Read off the item
// rather than the built card, because the feed picks a type before it has built
// anything. An item with both a player and an episode is a video: that is what
// the eye lands on.
func itemType(it core.Item) string {
	quoteVid := it.Quote != nil && it.Quote.Video != ""
	switch {
	case it.Video != "", quoteVid,
		biliVideoID(it.App, it.URL) != "",
		redgifID(it.URL, it.Title, it.Body) != "",
		ytLinkRe.MatchString(it.URL + " " + it.Title + " " + it.Body):
		return "video"
	case it.Audio != "":
		return "audio"
	default:
		return "text"
	}
}

// xForYouColor is the black the For You chip wears, so the two x chips are told
// apart at a glance: the blue one is the cached Following backlog, the black one
// is the timeline that is fetched on the spot.
const xForYouColor = "#000000"

// chipRow builds the chip row over a list: one group per axis (which service it
// came from, what it carries), each chip counting the unread items it stands
// for, so reading takes the counts down alongside the header's. Only one chip
// can be on, and picking it loads a page of its items alone, so every count is
// what that chip would bring rather than what it would leave.
//
// apps is the logged-in services, which the feed passes and the saved list
// doesn't. Given them, every one gets a chip whether or not it has anything on
// this page, because a source chip is also that service's status light — a
// service that failed to fetch has nothing to show and is exactly the one worth
// seeing. Without them (the saved list, read off disk, no service to be up or
// down) a group that would light up the whole list narrows nothing and is left
// out, and with both left out there is no row to draw.
//
// round is how many items the For You fetch just handed over, which is the one
// count that cannot be taken from a list on disk. It means nothing unless that
// chip is the one on.
func chipRow(t feedTally, apps []string, bad map[string]bool, xAuth bool, sel feedSel, q url.Values, round int) []filterGroup {
	var out []filterGroup
	var appChips []filterChip
	if len(apps) > 0 {
		// Every logged-in service, plus anything the list carries from one that
		// isn't (items cached before a logout are still items to filter by).
		live := map[string]bool{}
		for _, a := range apps {
			live[a] = true
			chip := filterChip{
				Kind: "app", Key: a, Label: appLabel(a), Color: appColor(a), Count: t.apps[a],
				State: "ok", Title: a + ": live",
			}
			if bad[a] {
				chip.State, chip.Title = "bad", a+": failed to load"
			}
			appChips = append(appChips, chip)
		}
		for a, n := range t.apps {
			if !live[a] {
				appChips = append(appChips, filterChip{Kind: "app", Key: a, Label: appLabel(a), Color: appColor(a), Count: n})
			}
		}
		sortChips(appChips)
	} else if len(t.apps) > 1 {
		for a, n := range t.apps {
			appChips = append(appChips, filterChip{Kind: "app", Key: a, Label: appLabel(a), Color: appColor(a), Count: n})
		}
		sortChips(appChips)
	}
	// x's other timeline is a chip of its own, next to the one for its backlog.
	// It is the one pick that fetches rather than reads, so there is always
	// another round of it to ask for.
	if xAuth {
		appChips = withForYou(appChips, sel, bad["x"], round)
	}
	if len(appChips) > 0 {
		out = append(out, filterGroup{Chips: appChips})
	}

	if len(t.types) > 1 {
		var g filterGroup
		for _, ty := range []string{"text", "video", "audio"} { // always this order, whatever the counts
			if n := t.types[ty]; n > 0 {
				g.Chips = append(g.Chips, filterChip{Kind: "type", Key: ty, Label: ty, Count: n})
			}
		}
		out = append(out, g)
	}

	for i := range out {
		for j := range out[i].Chips {
			c := &out[i].Chips[j]
			c.On = sel.Kind == c.Kind && sel.Key == c.Key
			c.Href = chipHref(q, feedSel{Kind: c.Kind, Key: c.Key})
			// Tapping the chip that is on puts the whole list back — except For
			// You, which is refetched on every visit, so tapping it again is
			// another round of it. The clear chip is the way off that one.
			if c.On && c.Kind != "x" {
				c.Href = chipHref(q, feedSel{})
			}
		}
	}
	return out
}

// withForYou slots x's For You chip in after x's own, or leaves the row alone
// when x has no chip there to sit beside.
//
// The chip is the icon on its own until a round has been fetched: from anywhere
// else there is nothing to count, since none of that timeline is kept. On the
// page the fetch served it states the round it brought, the way every other chip
// states its own number — and it is a status light there too, red when the fetch
// came back empty-handed rather than empty.
func withForYou(chips []filterChip, sel feedSel, failed bool, round int) []filterChip {
	foryou := filterChip{
		Kind: "x", Key: "foryou", Label: appLabel("x"), Color: xForYouColor,
		Uncounted: true, Title: "x's For You, fetched on the spot and not kept",
	}
	if sel.Kind == "x" {
		foryou.Uncounted, foryou.Count = false, round
		foryou.State, foryou.Title = "ok", "x: For You, this round"
		if failed {
			foryou.State, foryou.Title = "bad", "x: For You failed to load"
		}
	}
	for i, c := range chips {
		if c.Kind == "app" && c.Key == "x" {
			return append(chips[:i+1:i+1], append([]filterChip{foryou}, chips[i+1:]...)...)
		}
	}
	return chips
}

// chipHref is where a chip goes: this page's query with whichever filter param
// was on swapped for the chip's own, so the sort order (and the saved view)
// survives a pick. An empty sel is the link back to the whole list.
func chipHref(q url.Values, sel feedSel) string {
	out := url.Values{}
	for k, v := range q {
		switch k {
		case "app", "type", "x", "json": // the filter params this replaces, and one no page carries
		default:
			out[k] = v
		}
	}
	switch sel.Kind {
	case "app", "type", "x":
		out.Set(sel.Kind, sel.Key)
	}
	if len(out) == 0 {
		return "/"
	}
	return "/?" + out.Encode()
}

// orderHref is where the header's sort toggle goes: this page with the order
// turned around and everything else about it (the chip that is on, the view)
// left where it was. The order it lands on is always spelled out, even when it
// is the default one, so the page it opens states which way it runs whether or
// not the browser got as far as remembering.
func orderHref(q url.Values, asc bool) string {
	out := url.Values{}
	for k, v := range q {
		switch k {
		case "order", "json":
		default:
			out[k] = v
		}
	}
	if asc {
		out.Set("order", "desc")
	} else {
		out.Set("order", "asc")
	}
	return "/?" + out.Encode()
}

// parseSel reads the chip a request is asking for. x's For You wins over the
// rest: it is the one pick that is not a slice of the backlog.
func parseSel(q url.Values) feedSel {
	if q.Get("x") == "foryou" {
		return feedSel{Kind: "x", Key: "foryou"}
	}
	if a := q.Get("app"); a != "" {
		return feedSel{Kind: "app", Key: a}
	}
	switch ty := q.Get("type"); ty {
	case "text", "video", "audio":
		return feedSel{Kind: "type", Key: ty}
	}
	return feedSel{}
}

// selectItems narrows a list to the picked chip. The For You pick is not a
// slice of anything here — it is fetched instead — so it takes no items.
func selectItems(items []core.Item, sel feedSel) []core.Item {
	if !sel.on() {
		return items
	}
	out := make([]core.Item, 0, len(items))
	for _, it := range items {
		switch sel.Kind {
		case "app":
			if it.App == sel.Key {
				out = append(out, it)
			}
		case "type":
			if itemType(it) == sel.Key {
				out = append(out, it)
			}
		}
	}
	return out
}

// sortChips puts the busiest source first, alphabetical between ties, so the
// row is stable from load to load.
func sortChips(chips []filterChip) {
	sort.Slice(chips, func(i, j int) bool {
		if chips[i].Count != chips[j].Count {
			return chips[i].Count > chips[j].Count
		}
		return chips[i].Key < chips[j].Key
	})
}

// buildQuote shapes an embedded post into the nested card, clipped tighter than
// the parent so a long quote can't bury the post that quotes it.
func buildQuote(q core.Quote, clip int) *quoteData {
	author := q.Author
	if author == q.Source {
		author = ""
	}
	d := &quoteData{
		Source: q.Source,
		Author: author,
		URL:    q.URL,
		Video:  q.Video,
		Poster: q.Poster,
		VidLen: vidLen(q.VidSecs),
		Images: cardImages(q.Images),
	}
	if q.Text != "" {
		d.PreviewBody = template.HTML(preview(q.Text, clip))
		d.FullBody = template.HTML(linkify(q.Text))
	}
	return d
}

// vidLen formats a clip's length for the badge over its poster ("0:42",
// "1:05:03"), so the cost of a tap is visible before anything downloads. Blank
// when the app reported no duration: no badge is better than a wrong one.
func vidLen(secs int) string {
	if secs <= 0 {
		return ""
	}
	if h := secs / 3600; h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, secs/60%60, secs%60)
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

// clips is how much of a body and of a quoted post survive into the clipped
// preview panel, in runes.
type clips struct{ body, quote int }

var (
	listClips = clips{body: 220, quote: 140}
	// A swiped card owns the whole screen, so more text fits before anything
	// has to be expanded — but only about a screenful. The cap counts runes,
	// and CJK sets about twice as many lines per rune as Latin does, so it is
	// kept low enough that a dense Chinese post still leaves the footer above
	// the fold.
	swipeClips = clips{body: 420, quote: 180}
)

// needsExpand reports whether anything is clipped, i.e. there is more to reveal.
func needsExpand(body, title string, clip int) bool {
	return len([]rune(body)) > clip || len([]rune(title)) > clip
}

// preview renders the clipped panel: the first n runes, an ellipsis, and how
// many words expanding would still reveal. The count is the expand control
// itself, so the tap lands where the text stops rather than down in the
// footer. Text shorter than the clip is returned whole, which keeps preview
// and full identical for cards that need no expanding at all.
func preview(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return linkify(s)
	}
	return linkify(string(r[:n])) + `…<button class="rest" type="button" title="show the rest">` + restLabel(countWords(string(r[n:]))) + `</button>`
}

func restLabel(n int) string {
	if n == 1 {
		return "+1 word"
	}
	return fmt.Sprintf("+%d words", n)
}

// countWords counts runs of non-space text, except that each CJK rune counts
// on its own: Chinese and Japanese posts have no spaces, so a run-based count
// would report the whole remainder as one word. Stray punctuation never starts
// a word, so "你好,世界" is four and "hi, there" is two.
func countWords(s string) int {
	n, inWord := 0, false
	for _, r := range s {
		switch {
		case cjkRune(r):
			n++
			inWord = false
		case unicode.IsSpace(r):
			inWord = false
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
		default:
			if !inWord {
				n++
				inWord = true
			}
		}
	}
	return n
}

func cjkRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// redgifRe matches a redgifs watch (or embed) link. Reddit posts them as plain
// link posts, so the id in the URL is all there is to go on — the stream itself
// is resolved later, only if the footer's video button is tapped.
var redgifRe = regexp.MustCompile(`(?i)https?://(?:[\w-]+\.)?redgifs\.com/(?:watch|ifr)/([a-z0-9]{3,64})`)

// redgifID returns the first redgifs clip id among the given texts, blank when
// none of them links one.
func redgifID(texts ...string) string {
	for _, t := range texts {
		if m := redgifRe.FindStringSubmatch(t); m != nil {
			return strings.ToLower(m[1])
		}
	}
	return ""
}

// linkRe matches a URL plus any trailing punctuation (so "see http://x.com."
// renders the period as plain text, not part of the link).
var linkRe = regexp.MustCompile(`(https?://[^\s<>"']+)([.,;:!?)\]}"']*)`)

// linkify HTML-escapes text and turns embedded URLs into clickable links that
// open in a new tab; non-URL text is escaped as before.
func linkify(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range linkRe.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(escape(s[last:m[2]])) // text before the URL
		u := s[m[2]:m[3]]
		b.WriteString(`<a class="link" href="` + escape(u) + `" target="_blank" rel="noopener" title="` + escape(u) + `">` + escape(linkLabel(u)) + `</a>`)
		if m[4] >= 0 { // trailing punctuation kept as plain text
			b.WriteString(escape(s[m[4]:m[5]]))
		}
		last = m[1]
	}
	b.WriteString(escape(s[last:]))
	return b.String()
}

// linkLabel trims the scheme/www and shortens a URL for link text, keeping the
// full address in href and the title tooltip.
func linkLabel(u string) string {
	s := strings.TrimPrefix(u, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// writeJSONItems writes the merged feed as JSON, for API clients.
func writeJSONItems(w io.Writer, items []core.Item, failed []string) {
	type wireItem struct {
		App     string      `json:"app"`
		ID      string      `json:"id"`
		Title   string      `json:"title"`
		Body    string      `json:"body,omitempty"`
		Source  string      `json:"source,omitempty"`
		Author  string      `json:"author,omitempty"`
		URL     string      `json:"url,omitempty"`
		Age     string      `json:"age,omitempty"`
		TS      string      `json:"ts,omitempty"`
		Video   string      `json:"video,omitempty"`
		Poster  string      `json:"poster,omitempty"`
		VidSecs int         `json:"vidsecs,omitempty"`
		Audio   string      `json:"audio,omitempty"`
		Images  []string    `json:"images,omitempty"`
		Quote   *core.Quote `json:"quote,omitempty"`
	}
	out := make([]wireItem, 0, len(items))
	for _, it := range items {
		wi := wireItem{
			App:     it.App,
			ID:      it.ID,
			Title:   it.Title,
			Body:    it.Body,
			Source:  it.Source,
			Author:  it.Author,
			URL:     it.URL,
			Age:     it.Age,
			Video:   it.Video,
			Poster:  it.Poster,
			VidSecs: it.VidSecs,
			Audio:   it.Audio,
			Images:  it.Images,
			Quote:   it.Quote,
		}
		if !it.At.IsZero() {
			wi.TS = it.At.UTC().Format(time.RFC3339)
		}
		out = append(out, wi)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":  out,
		"failed": failed,
	})
}
