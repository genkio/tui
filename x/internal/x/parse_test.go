package x

import (
	"encoding/json"
	"testing"
)

const sampleCreatedAt = "Wed May 11 02:08:52 +0000 2011"

func userResult(name, screen string, legacy map[string]any) map[string]any {
	return map[string]any{
		"__typename": "Tweet",
		"rest_id":    legacy["rest_id"],
		"core": map[string]any{"user_results": map[string]any{"result": map[string]any{
			"core": map[string]any{"name": name, "screen_name": screen},
		}}},
		"legacy": legacy,
	}
}

func legacy(restID, fullText string, extra map[string]any) map[string]any {
	m := map[string]any{"rest_id": restID, "full_text": fullText, "created_at": sampleCreatedAt}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func item(entryID string, result map[string]any) map[string]any {
	return map[string]any{"entryId": entryID, "content": map[string]any{
		"entryType": "TimelineTimelineItem",
		"itemContent": map[string]any{
			"itemType":      "TimelineTweet",
			"tweet_results": map[string]any{"result": result},
		},
	}}
}

// buildSample exercises the parser shapes: a plain post (HTML entity + a
// trailing t.co link to strip), a repost (unwrap to the original, note the
// reposter), and a TweetWithVisibilityResults wrapper carrying a long-form note
// and a quoted post. A promoted entry and a cursor must be skipped.
func buildSample() []byte {
	plain := userResult("Alice", "alice", legacy("1", "hello &amp; world https://t.co/abc", map[string]any{
		"reply_count": 2, "retweet_count": 3, "favorite_count": 4, "quote_count": 1,
	}))

	original := userResult("Carol", "carol", legacy("20", "original text", map[string]any{"favorite_count": 99}))
	repost := userResult("Bob", "bob", legacy("2", "RT @carol: original", map[string]any{
		"retweeted_status_result": map[string]any{"result": original},
	}))

	quoted := userResult("Eve", "eve", legacy("30", "quoted body", nil))
	dave := userResult("Dave", "dave", legacy("3", "truncated", map[string]any{
		"quoted_status_result": map[string]any{"result": quoted},
	}))
	dave["note_tweet"] = map[string]any{"note_tweet_results": map[string]any{"result": map[string]any{"text": "a very long note body"}}}
	visibility := map[string]any{"__typename": "TweetWithVisibilityResults", "tweet": dave}

	promoted := userResult("Ad", "ad", legacy("9", "buy now", nil))
	cursor := map[string]any{"entryId": "cursor-bottom-x", "content": map[string]any{"entryType": "TimelineTimelineCursor"}}

	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{
			map[string]any{"type": "TimelineClearCache"},
			map[string]any{"type": "TimelineAddEntries", "entries": []any{
				item("tweet-1", plain),
				item("tweet-2", repost),
				item("tweet-3", visibility),
				item("promoted-9", promoted),
				cursor,
			}},
		},
	}}}}
	b, _ := json.Marshal(resp)
	return b
}

func TestParseTimeline(t *testing.T) {
	tweets, err := parseTimeline(buildSample())
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	if len(tweets) != 3 {
		t.Fatalf("got %d tweets, want 3 (promoted + cursor skipped)", len(tweets))
	}

	plain := tweets[0]
	if plain.Handle != "alice" || plain.Name != "Alice" {
		t.Errorf("plain author = %q/%q, want alice/Alice", plain.Handle, plain.Name)
	}
	if plain.Text != "hello & world" {
		t.Errorf("plain text = %q, want %q (entity decoded, t.co stripped)", plain.Text, "hello & world")
	}
	if plain.Replies != 2 || plain.Reposts != 3 || plain.Likes != 4 || plain.Quotes != 1 {
		t.Errorf("plain counts = %d/%d/%d/%d, want 2/3/4/1", plain.Replies, plain.Reposts, plain.Likes, plain.Quotes)
	}
	if plain.URL != "https://x.com/alice/status/1" {
		t.Errorf("plain url = %q", plain.URL)
	}
	if plain.RepostBy != "" {
		t.Errorf("plain RepostBy = %q, want empty", plain.RepostBy)
	}

	repost := tweets[1]
	if repost.Handle != "carol" || repost.Text != "original text" {
		t.Errorf("repost author/text = %q/%q, want carol/original text", repost.Handle, repost.Text)
	}
	if repost.RepostBy != "Bob" {
		t.Errorf("repost RepostBy = %q, want Bob", repost.RepostBy)
	}
	if repost.Likes != 99 {
		t.Errorf("repost likes = %d, want 99 (original's count)", repost.Likes)
	}
	if repost.URL != "https://x.com/carol/status/20" {
		t.Errorf("repost url = %q", repost.URL)
	}

	note := tweets[2]
	if note.Handle != "dave" {
		t.Errorf("note author = %q, want dave (visibility wrapper unwrapped)", note.Handle)
	}
	if note.Text != "a very long note body" {
		t.Errorf("note text = %q, want the note body (overrides truncated legacy)", note.Text)
	}
	if note.Quoted == nil || note.Quoted.Handle != "eve" || note.Quoted.Text != "quoted body" {
		t.Errorf("note quoted = %+v, want eve/quoted body", note.Quoted)
	}
}

func TestParseTimelineError(t *testing.T) {
	_, err := parseTimeline([]byte(`{"errors":[{"message":"Bad guest token"}]}`))
	if err == nil {
		t.Fatal("expected an error from a GraphQL errors payload")
	}
}
