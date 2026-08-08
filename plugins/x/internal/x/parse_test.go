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

	// x.com hangs quoted_status_result off the result, not off legacy.
	quoted := userResult("Eve", "eve", legacy("30", "quoted body https://t.co/def", map[string]any{
		"extended_entities": map[string]any{"media": []any{map[string]any{
			"type":            "video",
			"media_url_https": "https://pbs.twimg.com/ext_tw_video_thumb/30/poster.jpg",
			"video_info": map[string]any{"variants": []any{
				map[string]any{"content_type": "video/mp4", "bitrate": 832000, "url": "https://video.twimg.com/ext_tw_video/30/quoted.mp4"},
			}},
		}}},
	}))
	dave := userResult("Dave", "dave", legacy("3", "truncated", nil))
	dave["quoted_status_result"] = map[string]any{"result": quoted}
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
		t.Fatalf("note quoted = %+v, want eve/quoted body", note.Quoted)
	}
	if note.Quoted.Name != "Eve" || note.Quoted.URL != "https://x.com/eve/status/30" {
		t.Errorf("note quoted author/url = %q/%q, want Eve/https://x.com/eve/status/30", note.Quoted.Name, note.Quoted.URL)
	}
	if note.Quoted.VideoURL != "https://video.twimg.com/ext_tw_video/30/quoted.mp4" {
		t.Errorf("quoted video = %q, want the quoted post's mp4", note.Quoted.VideoURL)
	}
	if note.Quoted.VideoPoster != "https://pbs.twimg.com/ext_tw_video_thumb/30/poster.jpg" {
		t.Errorf("quoted poster = %q", note.Quoted.VideoPoster)
	}
	// The parent keeps its own (absent) media: the quote's video is not adopted.
	if note.VideoURL != "" {
		t.Errorf("parent video = %q, want empty (the video belongs to the quote)", note.VideoURL)
	}
}

// A quote nested under legacy is the older shape; it must still parse, since
// the timeline endpoints don't all move at once.
func TestParseTimelineQuoteUnderLegacy(t *testing.T) {
	quoted := userResult("Eve", "eve", legacy("30", "quoted body", nil))
	parent := userResult("Dave", "dave", legacy("3", "see this", map[string]any{
		"quoted_status_result": map[string]any{"result": quoted},
	}))
	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{map[string]any{"type": "TimelineAddEntries", "entries": []any{item("tweet-3", parent)}}},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("got %d tweets, want 1", len(tweets))
	}
	if q := tweets[0].Quoted; q == nil || q.Handle != "eve" || q.Text != "quoted body" {
		t.Errorf("quoted = %+v, want eve/quoted body from the legacy slot", q)
	}
}

// The post body no longer swallows the quote: it stays structured so renderers
// can draw it as a nested card.
func TestToItemCarriesQuote(t *testing.T) {
	tw := Tweet{
		ID: "3", Handle: "dave", Name: "Dave", Text: "see this",
		Quoted: &QuotedTweet{
			Handle: "eve", Name: "Eve", Text: "quoted body",
			URL: "https://x.com/eve/status/30", VideoURL: "https://video.twimg.com/q.mp4",
		},
	}
	it := ToItem(tw)
	if it.Body != "see this" {
		t.Errorf("body = %q, want just the post's own text", it.Body)
	}
	if it.Quote == nil {
		t.Fatal("expected a structured quote")
	}
	if it.Quote.Source != "@eve" || it.Quote.Author != "Eve" || it.Quote.Text != "quoted body" {
		t.Errorf("quote = %+v", it.Quote)
	}
	if it.Quote.URL != "https://x.com/eve/status/30" || it.Quote.Video != "https://video.twimg.com/q.mp4" {
		t.Errorf("quote url/video = %q/%q", it.Quote.URL, it.Quote.Video)
	}
	if ToItem(Tweet{ID: "4", Text: "no quote"}).Quote != nil {
		t.Error("a post with no quote must carry no quote")
	}
}

func TestParseTimelineError(t *testing.T) {
	_, err := parseTimeline([]byte(`{"errors":[{"message":"Bad guest token"}]}`))
	if err == nil {
		t.Fatal("expected an error from a GraphQL errors payload")
	}
}

func TestParseTimelineVideo(t *testing.T) {
	video := userResult("Vera", "vera", legacy("50", "watch this https://t.co/xyz", map[string]any{
		"extended_entities": map[string]any{"media": []any{
			map[string]any{"type": "photo", "media_url_https": "https://pbs.twimg.com/media/photo.jpg"},
			map[string]any{
				"type":            "video",
				"media_url_https": "https://pbs.twimg.com/ext_tw_video_thumb/50/pu/img/poster.jpg",
				"video_info": map[string]any{"variants": []any{
					map[string]any{"content_type": "application/x-mpegURL", "url": "https://video.twimg.com/ext_tw_video/50/pu/pl/list.m3u8"},
					map[string]any{"content_type": "video/mp4", "bitrate": 632000, "url": "https://video.twimg.com/ext_tw_video/50/pu/vid/avc1/320x568/low.mp4"},
					map[string]any{"content_type": "video/mp4", "bitrate": 2176000, "url": "https://video.twimg.com/ext_tw_video/50/pu/vid/avc1/720x1280/high.mp4"},
				}},
			},
		}},
	}))
	gif := userResult("Gil", "gil", legacy("51", "lol", map[string]any{
		"extended_entities": map[string]any{"media": []any{
			map[string]any{
				"type":            "animated_gif",
				"media_url_https": "https://pbs.twimg.com/tweet_video_thumb/51.jpg",
				"video_info": map[string]any{"variants": []any{
					map[string]any{"content_type": "video/mp4", "bitrate": 0, "url": "https://video.twimg.com/tweet_video/51.mp4"},
				}},
			},
		}},
	}))
	plain := userResult("Pat", "pat", legacy("52", "no media", nil))

	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{map[string]any{"type": "TimelineAddEntries", "entries": []any{
			item("tweet-50", video), item("tweet-51", gif), item("tweet-52", plain),
		}}},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	if len(tweets) != 3 {
		t.Fatalf("got %d tweets, want 3", len(tweets))
	}
	if got, want := tweets[0].VideoURL, "https://video.twimg.com/ext_tw_video/50/pu/vid/avc1/720x1280/high.mp4"; got != want {
		t.Errorf("video url = %q, want highest-bitrate mp4 %q", got, want)
	}
	if got, want := tweets[0].VideoPoster, "https://pbs.twimg.com/ext_tw_video_thumb/50/pu/img/poster.jpg"; got != want {
		t.Errorf("video poster = %q, want %q", got, want)
	}
	if got, want := tweets[1].VideoURL, "https://video.twimg.com/tweet_video/51.mp4"; got != want {
		t.Errorf("gif url = %q, want %q (bitrate-0 variant still picked)", got, want)
	}
	if tweets[2].VideoURL != "" || tweets[2].VideoPoster != "" {
		t.Errorf("plain tweet video = %q/%q, want empty", tweets[2].VideoURL, tweets[2].VideoPoster)
	}
	// The photo entity sitting next to the video is a thumbnail of neither: it is
	// its own attachment, and belongs in Images.
	if got := tweets[0].Images; len(got) != 1 || got[0] != "https://pbs.twimg.com/media/photo.jpg" {
		t.Errorf("images = %v, want the one photo entity", got)
	}
	if len(tweets[1].Images) != 0 || len(tweets[2].Images) != 0 {
		t.Errorf("a GIF and a text post carry no photos: %v / %v", tweets[1].Images, tweets[2].Images)
	}
}

func TestParseTimelinePhotos(t *testing.T) {
	shot := func(u string) map[string]any {
		return map[string]any{"type": "photo", "media_url_https": u}
	}
	post := userResult("Pia", "pia", legacy("70", "album https://t.co/xyz", map[string]any{
		"extended_entities": map[string]any{"media": []any{
			shot("https://pbs.twimg.com/media/one.jpg"),
			shot("https://pbs.twimg.com/media/two.jpg"),
		}},
	}))
	quoted := userResult("Quinn", "quinn", legacy("71", "mine too", map[string]any{
		"extended_entities": map[string]any{"media": []any{shot("https://pbs.twimg.com/media/q.jpg")}},
	}))
	post["quoted_status_result"] = map[string]any{"result": quoted}

	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{map[string]any{"type": "TimelineAddEntries", "entries": []any{item("tweet-70", post)}}},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	got := tweets[0].Images
	if len(got) != 2 || got[0] != "https://pbs.twimg.com/media/one.jpg" || got[1] != "https://pbs.twimg.com/media/two.jpg" {
		t.Errorf("images = %v, want both photos in x's order", got)
	}
	if q := tweets[0].Quoted; q == nil || len(q.Images) != 1 || q.Images[0] != "https://pbs.twimg.com/media/q.jpg" {
		t.Errorf("quoted images = %+v, want the quoted post's own photo", q)
	}
	// The photos travel to the shared item shape, parent and quote alike.
	it := ToItem(tweets[0])
	if len(it.Images) != 2 || it.Quote == nil || len(it.Quote.Images) != 1 {
		t.Errorf("ToItem lost photos: %v / %+v", it.Images, it.Quote)
	}
}
