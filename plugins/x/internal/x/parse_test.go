package x

import (
	"encoding/json"
	"fmt"
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
			"video_info": map[string]any{"duration_millis": 12000, "variants": []any{
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
	if note.Quoted.VideoSecs != 12 {
		t.Errorf("quoted video secs = %d, want 12", note.Quoted.VideoSecs)
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

// module wraps posts the way For You groups a conversation: one entry whose
// content is a list of items rather than a single post.
func module(entryID string, results ...map[string]any) map[string]any {
	items := make([]any, 0, len(results))
	for i, r := range results {
		items = append(items, map[string]any{
			"entryId": fmt.Sprintf("%s-%d", entryID, i),
			"item":    map[string]any{"itemContent": map[string]any{"itemType": "TimelineTweet", "tweet_results": map[string]any{"result": r}}},
		})
	}
	return map[string]any{"entryId": entryID, "content": map[string]any{
		"entryType": "TimelineTimelineModule",
		"items":     items,
	}}
}

// For You injects conversations as modules. Reading only the standalone entries
// throws most of a page away, which is why that tab came back nearly empty.
func TestParseTimelineReadsModules(t *testing.T) {
	solo := userResult("Alice", "alice", legacy("1", "a standalone post", nil))
	root := userResult("Bob", "bob", legacy("2", "conversation root", nil))
	reply := userResult("Carol", "carol", legacy("3", "the reply", nil))
	ad := userResult("Ad", "ad", legacy("4", "buy now", nil))
	later := userResult("Dan", "dan", legacy("5", "appended later", nil))

	// A module item can hold a user card rather than a post; it has no tweet.
	whoToFollow := map[string]any{"entryId": "who-to-follow-1", "content": map[string]any{
		"entryType": "TimelineTimelineModule",
		"items": []any{map[string]any{
			"entryId": "who-to-follow-1-0",
			"item":    map[string]any{"itemContent": map[string]any{"itemType": "TimelineUser"}},
		}},
	}}

	// A promoted post inside a module is still an ad.
	promoModule := module("home-conversation-9", ad)
	promoModule["content"].(map[string]any)["items"].([]any)[0].(map[string]any)["entryId"] = "promoted-tweet-4"

	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{
			map[string]any{"type": "TimelineAddEntries", "entries": []any{
				item("tweet-1", solo),
				module("home-conversation-2", root, reply),
				whoToFollow,
				promoModule,
				map[string]any{"entryId": "cursor-bottom-x", "content": map[string]any{"entryType": "TimelineTimelineCursor"}},
			}},
			map[string]any{"type": "TimelineAddToModule", "moduleItems": []any{
				map[string]any{
					"entryId": "home-conversation-2-2",
					"item":    map[string]any{"itemContent": map[string]any{"itemType": "TimelineTweet", "tweet_results": map[string]any{"result": later}}},
				},
			}},
		},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	var got []string
	for _, tw := range tweets {
		got = append(got, tw.ID)
	}
	want := []string{"1", "2", "3", "5"} // the ad and the user card contribute nothing
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// A post can appear both on its own and inside a module; it should show once.
func TestParseTimelineDedupesAcrossModules(t *testing.T) {
	post := userResult("Alice", "alice", legacy("1", "hello", nil))
	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{map[string]any{"type": "TimelineAddEntries", "entries": []any{
			item("tweet-1", post),
			module("home-conversation-1", post),
		}}},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	if len(tweets) != 1 {
		t.Errorf("got %d tweets, want the repeat collapsed into 1", len(tweets))
	}
}

func TestParseTimelineError(t *testing.T) {
	_, err := parseTimeline([]byte(`{"errors":[{"message":"Bad guest token"}]}`))
	if err == nil {
		t.Fatal("expected an error from a GraphQL errors payload")
	}
}

func urlEnt(short, full string) map[string]any {
	return map[string]any{"url": short, "expanded_url": full}
}

// x rewrites every link an author types into a t.co shortlink. Left alone, one
// at the end of a post looks exactly like the shortlink x appends for media,
// and the post loses the link it was written to share.
func TestParseTimelineExpandsLinks(t *testing.T) {
	// the shape of https://x.com/badlogicgames/status/2086189428012093655
	article := "https://blog.senko.net/code-was-never-the-hard-part-is-an-insult-to-all-programmers"
	link := userResult("Mario", "badlogicgames", legacy("80", "recommended reading.\n\nhttps://t.co/igyB7oofie", map[string]any{
		"entities": map[string]any{"urls": []any{urlEnt("https://t.co/igyB7oofie", article)}},
	}))

	// A link plus attached media: the author's link expands, x's own media
	// shortlink still goes.
	both := userResult("Ann", "ann", legacy("81", "see https://t.co/aaa https://t.co/bbb", map[string]any{
		"entities": map[string]any{"urls": []any{urlEnt("https://t.co/aaa", "https://real.example/post")}},
		"extended_entities": map[string]any{"media": []any{
			map[string]any{"type": "photo", "media_url_https": "https://pbs.twimg.com/media/p.jpg"},
		}},
	}))

	// A long post keeps its links in its own entity set, not legacy's.
	note := userResult("Nora", "nora", legacy("82", "truncated https://t.co/ccc", nil))
	note["note_tweet"] = map[string]any{"note_tweet_results": map[string]any{"result": map[string]any{
		"text":       "the whole essay, see https://t.co/ccc",
		"entity_set": map[string]any{"urls": []any{urlEnt("https://t.co/ccc", "https://essay.example/full")}},
	}}}

	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{map[string]any{"type": "TimelineAddEntries", "entries": []any{
			item("tweet-80", link), item("tweet-81", both), item("tweet-82", note),
		}}},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	if want := "recommended reading.\n\n" + article; tweets[0].Text != want {
		t.Errorf("text = %q, want %q", tweets[0].Text, want)
	}
	if want := "see https://real.example/post"; tweets[1].Text != want {
		t.Errorf("text = %q, want %q (author's link kept, media shortlink dropped)", tweets[1].Text, want)
	}
	if want := "the whole essay, see https://essay.example/full"; tweets[2].Text != want {
		t.Errorf("note text = %q, want %q", tweets[2].Text, want)
	}
}

// A quoted post's links are the quoting author's to read too.
func TestParseTimelineExpandsQuotedLinks(t *testing.T) {
	quoted := userResult("Eve", "eve", legacy("90", "my post https://t.co/qqq", map[string]any{
		"entities": map[string]any{"urls": []any{urlEnt("https://t.co/qqq", "https://eve.example/thing")}},
	}))
	parent := userResult("Dave", "dave", legacy("91", "look https://t.co/perma", nil))
	parent["quoted_status_result"] = map[string]any{"result": quoted}

	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{map[string]any{"type": "TimelineAddEntries", "entries": []any{item("tweet-91", parent)}}},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	// The parent's trailing shortlink is x's permalink to the quote; it goes.
	if tweets[0].Text != "look" {
		t.Errorf("parent text = %q, want the quote permalink dropped", tweets[0].Text)
	}
	if q := tweets[0].Quoted; q == nil || q.Text != "my post https://eve.example/thing" {
		t.Errorf("quoted text = %+v, want its link expanded", q)
	}
}

// block builds one Draft.js paragraph the way x lays an article out.
func block(kind, text string) map[string]any {
	return map[string]any{"type": kind, "text": text, "key": "k" + text}
}

// A long post's own text is just the t.co link back to itself, which cleanText
// strips to nothing: everything readable has to come from the article.
func TestParseTimelineArticle(t *testing.T) {
	art := map[string]any{
		"rest_id":      "700",
		"title":        "DeepSeek &amp; the Responses API",
		"preview_text": "the preview x shows in a timeline card",
		"cover_media": map[string]any{
			"media_info": map[string]any{"original_img_url": "https://pbs.twimg.com/media/cover.jpg"},
		},
		"content_state": map[string]any{"blocks": []any{
			block("unstyled", "First paragraph."),
			block("header-two", "A section"),
			block("unordered-list-item", "a bullet"),
			block("atomic", ""), // an inline image: no text of its own
			block("unstyled", "Last paragraph."),
		}},
		"media_entities": []any{
			map[string]any{"media_info": map[string]any{"original_img_url": "https://pbs.twimg.com/media/inline.jpg"}},
			map[string]any{"media_info": map[string]any{"media_url_https": "https://video.twimg.com/x.mp4"}}, // a video: no still
		},
	}
	post := userResult("Sal", "saladdayyy", legacy("70", "https://t.co/abc", nil))
	post["article"] = map[string]any{"article_results": map[string]any{"result": art}}

	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{map[string]any{"type": "TimelineAddEntries", "entries": []any{item("tweet-70", post)}}},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("got %d tweets, want 1", len(tweets))
	}
	tw := tweets[0]
	if tw.Text != "" {
		t.Errorf("post text = %q, want empty (it held only the t.co link)", tw.Text)
	}
	if tw.Article == nil {
		t.Fatal("expected an article")
	}
	if tw.Article.Title != "DeepSeek & the Responses API" {
		t.Errorf("title = %q, want the entity decoded", tw.Article.Title)
	}
	want := "First paragraph.\n\nA section\n\n- a bullet\n\nLast paragraph."
	if tw.Article.Text != want {
		t.Errorf("body = %q, want %q", tw.Article.Text, want)
	}
	if tw.Article.Cover != "https://pbs.twimg.com/media/cover.jpg" {
		t.Errorf("cover = %q", tw.Article.Cover)
	}
	if got := tw.Article.Images; len(got) != 1 || got[0] != "https://pbs.twimg.com/media/inline.jpg" {
		t.Errorf("images = %v, want just the still (a video entity has no image url)", got)
	}

	// The headline and body stay distinct, which is what makes the card render a
	// headline at all, and the cover leads the images.
	it := ToItem(tw)
	if it.Title != tw.Article.Title || it.Body != tw.Article.Text {
		t.Errorf("item title/body = %q / %q", it.Title, it.Body)
	}
	if len(it.Images) != 2 || it.Images[0] != "https://pbs.twimg.com/media/cover.jpg" {
		t.Errorf("item images = %v, want the cover first", it.Images)
	}
}

// When the timeline answers without content_state there is only the preview, and
// it must not read as if it were the whole piece.
func TestParseTimelineArticlePreviewOnly(t *testing.T) {
	art := map[string]any{
		"rest_id":      "701",
		"title":        "A long read",
		"preview_text": "only the opening lines",
	}
	post := userResult("Sal", "saladdayyy", legacy("71", "https://t.co/abc", nil))
	post["article"] = map[string]any{"article_results": map[string]any{"result": art}}
	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{map[string]any{"type": "TimelineAddEntries", "entries": []any{item("tweet-71", post)}}},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	a := tweets[0].Article
	if a == nil || a.Title != "A long read" {
		t.Fatalf("article = %+v", a)
	}
	if a.Text != "only the opening lines…" {
		t.Errorf("text = %q, want the preview marked as cut short", a.Text)
	}
}

// A titleless article envelope carries nothing to show, so the post falls back
// to its ordinary text rather than rendering an empty headline.
func TestParseTimelineArticleWithoutTitle(t *testing.T) {
	post := userResult("Sal", "saladdayyy", legacy("72", "still a normal post", nil))
	post["article"] = map[string]any{"article_results": map[string]any{"result": map[string]any{"rest_id": "702"}}}
	resp := map[string]any{"data": map[string]any{"home": map[string]any{"home_timeline_urt": map[string]any{
		"instructions": []any{map[string]any{"type": "TimelineAddEntries", "entries": []any{item("tweet-72", post)}}},
	}}}}
	b, _ := json.Marshal(resp)

	tweets, err := parseTimeline(b)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	if tweets[0].Article != nil {
		t.Errorf("article = %+v, want none", tweets[0].Article)
	}
	if it := ToItem(tweets[0]); it.Title != "still a normal post" {
		t.Errorf("title = %q, want the post's own text", it.Title)
	}
}

func TestParseTimelineNoArticle(t *testing.T) {
	tweets, err := parseTimeline(buildSample())
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	for _, tw := range tweets {
		if tw.Article != nil {
			t.Errorf("%s: ordinary posts must carry no article", tw.ID)
		}
	}
	// An ordinary post still puts its text in both title and body, so the card
	// collapses them into one block.
	it := ToItem(tweets[0])
	if it.Title != it.Body || it.Title == "" {
		t.Errorf("title/body = %q / %q, want the same text", it.Title, it.Body)
	}
}

func TestParseTimelineVideo(t *testing.T) {
	video := userResult("Vera", "vera", legacy("50", "watch this https://t.co/xyz", map[string]any{
		"extended_entities": map[string]any{"media": []any{
			map[string]any{"type": "photo", "media_url_https": "https://pbs.twimg.com/media/photo.jpg"},
			map[string]any{
				"type":            "video",
				"media_url_https": "https://pbs.twimg.com/ext_tw_video_thumb/50/pu/img/poster.jpg",
				"video_info": map[string]any{"duration_millis": 30983, "variants": []any{
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
	if got, want := tweets[0].VideoSecs, 31; got != want {
		t.Errorf("video secs = %d, want %d (30983ms rounded)", got, want)
	}
	if got, want := tweets[1].VideoURL, "https://video.twimg.com/tweet_video/51.mp4"; got != want {
		t.Errorf("gif url = %q, want %q (bitrate-0 variant still picked)", got, want)
	}
	// No duration_millis on the GIF: length unknown rather than zero-length.
	if got := tweets[1].VideoSecs; got != 0 {
		t.Errorf("gif secs = %d, want 0 when x reports no duration", got)
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
