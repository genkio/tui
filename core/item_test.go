package core

import (
	"testing"
	"time"
)

func TestAgeToDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"now", 0, true},
		{"just now", 0, true},
		{"30s", 30 * time.Second, true},
		{"5m", 5 * time.Minute, true},
		{"2h", 2 * time.Hour, true},
		{"3d", 3 * 24 * time.Hour, true},
		{"1w", 7 * 24 * time.Hour, true},
		{"2mo", 2 * 30 * 24 * time.Hour, true}, // month, not minute
		{"1y", 365 * 24 * time.Hour, true},
		{"20 min", 20 * time.Minute, true},
		{"", 0, false},
		{"Jul 1", 0, false}, // absolute date on an old item: unparseable
	}
	for _, c := range cases {
		got, ok := ageToDuration(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ageToDuration(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestExtractJSONArray(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`[{"id":"1"}]`, `[{"id":"1"}]`},
		{"make: chatter\n[{\"id\":\"1\"}]\n", `[{"id":"1"}]`},
		{"only an error, no json", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := string(extractJSONArray([]byte(c.in)))
		if got != c.want {
			t.Errorf("extractJSONArray(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMergeSortMixed is the core guarantee: items carrying an absolute ts and
// items carrying only a relative age interleave into one newest-first order,
// and anything without a resolvable time sinks to the bottom.
func TestMergeSortMixed(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	// x/folo supply ts; inoreader supplies age only; one item has neither.
	out := []byte(`[
		{"app":"x","id":"a","title":"x 30m ago","ts":"2026-07-03T11:30:00Z"},
		{"app":"inoreader","id":"b","title":"ino 10m ago","age":"10m"},
		{"app":"folo","id":"c","title":"folo 2h ago","ts":"2026-07-03T10:00:00Z"},
		{"app":"inoreader","id":"d","title":"ino no time","age":"Jul 1"}
	]`)
	items, err := ParseItems(out, now)
	if err != nil {
		t.Fatalf("ParseItems: %v", err)
	}
	MergeSort(items)

	gotOrder := make([]string, len(items))
	for i, it := range items {
		gotOrder[i] = it.ID
	}
	// b (10m) newest, then a (30m), then c (2h); d (no time) last.
	want := []string{"b", "a", "c", "d"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("merged order = %v, want %v", gotOrder, want)
		}
	}
}

// An embedded post has to survive the subprocess hop (an app's --json into the
// launcher) and the saved store, both of which go through Wire.
func TestQuoteSurvivesTheWire(t *testing.T) {
	out := []byte(`[{"app":"x","id":"3","title":"see this","body":"see this","source":"@dave",
		"quote":{"source":"@eve","author":"Eve","text":"quoted body","url":"https://x.com/eve/status/30","video":"https://video.twimg.com/q.mp4"}}]`)
	items, err := ParseItems(out, time.Now())
	if err != nil {
		t.Fatalf("ParseItems: %v", err)
	}
	q := items[0].Quote
	if q == nil {
		t.Fatal("quote dropped on the way in")
	}
	if q.Source != "@eve" || q.Text != "quoted body" || q.Video != "https://video.twimg.com/q.mp4" {
		t.Errorf("quote = %+v", q)
	}
	if got := q.Inline(); got != "quoting @eve: quoted body" {
		t.Errorf("Inline() = %q", got)
	}
	if items[0].Wire().Quote != q {
		t.Error("quote dropped on the way back out")
	}
	// A post with no quote stays quote-free rather than gaining an empty one.
	plain, err := ParseItems([]byte(`[{"app":"x","id":"4","title":"solo"}]`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plain[0].Quote != nil {
		t.Error("a quote-free post must not gain one")
	}
}

func TestItemKeyDistinguishesApps(t *testing.T) {
	// The same numeric id from two services must not collide.
	a := Item{App: "x", ID: "123"}
	b := Item{App: "folo", ID: "123"}
	if a.Key() == b.Key() {
		t.Errorf("keys collided across apps: %q", a.Key())
	}
}
