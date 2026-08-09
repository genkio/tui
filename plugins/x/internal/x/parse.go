package x

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
)

// twitterTimeLayout matches the legacy created_at, e.g. "Wed May 11 02:08:52 +0000 2011".
const twitterTimeLayout = "Mon Jan 02 15:04:05 -0700 2006"

func parseTimeline(body []byte) ([]Tweet, error) {
	var r apiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decoding timeline: %w", err)
	}
	if len(r.Errors) > 0 {
		return nil, fmt.Errorf("x.com: %s", r.Errors[0].Message)
	}

	var tweets []Tweet
	seen := map[string]bool{} // a module can repeat a post that also stands alone
	add := func(t *Tweet) {
		if t == nil || seen[t.ID] {
			return
		}
		seen[t.ID] = true
		tweets = append(tweets, *t)
	}
	for _, ins := range r.Data.Home.HomeTimelineURT.Instructions {
		switch ins.Type {
		case "TimelineAddEntries":
			for _, e := range ins.Entries {
				if strings.HasPrefix(e.EntryID, "promoted") {
					continue // skip ads
				}
				if e.Content.ItemContent.ItemType == "TimelineTweet" {
					add(e.Content.ItemContent.tweet())
					continue
				}
				// a module: For You wraps conversations in these, so skipping
				// them would drop much of a page
				for _, mi := range e.Content.Items {
					add(mi.tweet())
				}
			}
		case "TimelineAddToModule":
			// posts appended to a module already on the timeline
			for _, mi := range ins.ModuleItems {
				add(mi.tweet())
			}
		}
	}
	return tweets, nil
}

func (ic itemContent) tweet() *Tweet {
	if ic.ItemType != "TimelineTweet" {
		return nil // a module can also hold users (who-to-follow) or prompts
	}
	res := ic.TweetResults.Result.normalize()
	if res == nil || res.Legacy == nil {
		return nil // tombstone / unavailable post
	}
	tw := res.toTweet()
	return &tw
}

func (mi moduleItem) tweet() *Tweet {
	if strings.HasPrefix(mi.EntryID, "promoted") {
		return nil
	}
	return mi.Item.ItemContent.tweet()
}

// normalize unwraps the TweetWithVisibilityResults envelope to the plain Tweet.
func (t *tweetResult) normalize() *tweetResult {
	if t == nil {
		return nil
	}
	if t.TypeName == "TweetWithVisibilityResults" && t.Tweet != nil {
		return t.Tweet.normalize()
	}
	return t
}

func (t *tweetResult) author() (name, handle string) {
	c := t.Core.UserResults.Result.Core
	return c.Name, c.ScreenName
}

// text is the post body together with the URL entities belonging to it: a
// long-form note carries its own set, and pairing a body with the wrong set
// would leave its t.co links unexpanded.
func (t *tweetResult) text() (string, []urlEntity) {
	if t.NoteTweet != nil {
		if r := t.NoteTweet.NoteTweetResults.Result; r.Text != "" {
			return r.Text, r.EntitySet.URLs // long-form; legacy.full_text is truncated
		}
	}
	if t.Legacy != nil {
		return t.Legacy.FullText, t.Legacy.Entities.URLs
	}
	return "", nil
}

func (t *tweetResult) toTweet() Tweet {
	// A repost wraps the original under legacy.retweeted_status_result; show the
	// original author and body, noting who put it on your timeline.
	repostBy := ""
	src := t
	if t.Legacy != nil && t.Legacy.RetweetedStatusResult != nil && t.Legacy.RetweetedStatusResult.Result != nil {
		if orig := t.Legacy.RetweetedStatusResult.Result.normalize(); orig != nil && orig.Legacy != nil {
			name, _ := t.author()
			repostBy = name
			src = orig
		}
	}

	name, handle := src.author()
	lg := src.Legacy
	tw := Tweet{
		ID:       src.RestID,
		Handle:   handle,
		Name:     name,
		Text:     cleanText(src.text()),
		Replies:  lg.ReplyCount,
		Reposts:  lg.RetweetCount,
		Likes:    lg.FavoriteCount,
		Quotes:   lg.QuoteCount,
		RepostBy: repostBy,
	}
	if ts, err := time.Parse(twitterTimeLayout, lg.CreatedAt); err == nil {
		tw.CreatedAt = ts
		tw.Age = relAge(time.Since(ts))
	}
	if handle != "" && src.RestID != "" {
		tw.URL = "https://x.com/" + handle + "/status/" + src.RestID
	}
	tw.Quoted = src.quoted()
	tw.VideoURL, tw.VideoPoster, tw.VideoSecs = lg.video()
	tw.Images = lg.photos()
	tw.Article = src.article()
	return tw
}

// article flattens a long post. Its body lives outside the 280-char text (which
// carries only a t.co link back to it), so without this the post reads as blank.
// The timeline only carries content_state when the request asks for it; when it
// doesn't, preview_text is all there is and the text is marked as cut short.
func (t *tweetResult) article() *Article {
	if t.Article == nil {
		return nil
	}
	res := t.Article.ArticleResults.Result
	if res == nil || res.Title == "" {
		return nil
	}
	a := &Article{Title: cleanText(res.Title, nil), Cover: res.cover()}
	if body := res.ContentState.text(); body != "" {
		a.Text = body
	} else if p := cleanText(res.PreviewText, nil); p != "" {
		a.Text = p + "…" // a preview, not the whole piece: don't pretend otherwise
	}
	a.Images = res.images()
	return a
}

// text joins an article's Draft.js blocks into paragraphs. Media blocks
// ("atomic") carry no text and are dropped; their images come from
// media_entities instead.
func (cs *articleContentState) text() string {
	var parts []string
	for _, b := range cs.Blocks {
		s := strings.TrimSpace(html.UnescapeString(b.Text))
		if s == "" {
			continue
		}
		if strings.HasSuffix(b.Type, "list-item") {
			s = "- " + s
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n\n")
}

// cover reads the header image. x returns it bare on some responses and wrapped
// in a result envelope on others, so both spellings are tried.
func (r *articleResult) cover() string {
	if r.CoverMedia != nil && r.CoverMedia.MediaInfo.OriginalImgURL != "" {
		return r.CoverMedia.MediaInfo.OriginalImgURL
	}
	if r.CoverMediaResults != nil && r.CoverMediaResults.Result != nil {
		return r.CoverMediaResults.Result.MediaInfo.OriginalImgURL
	}
	return ""
}

// images are the stills laid out through the body. Video entities carry no
// original_img_url, so they fall away here.
func (r *articleResult) images() []string {
	var out []string
	for _, m := range r.MediaEntities {
		if u := m.MediaInfo.OriginalImgURL; u != "" {
			out = append(out, u)
		}
	}
	return out
}

// quoted flattens the post this one quotes. x.com hangs quoted_status_result off
// the result itself, not off legacy the way retweeted_status_result is; the
// legacy slot is read as a fallback in case the shape moves back.
func (t *tweetResult) quoted() *QuotedTweet {
	res := t.QuotedStatusResult
	if res == nil && t.Legacy != nil {
		res = t.Legacy.QuotedStatusResult
	}
	if res == nil || res.Result == nil {
		return nil
	}
	q := res.Result.normalize()
	if q == nil || q.Legacy == nil {
		return nil // tombstone / unavailable post
	}
	name, handle := q.author()
	qt := &QuotedTweet{Name: name, Handle: handle, Text: cleanText(q.text())}
	if handle != "" && q.RestID != "" {
		qt.URL = "https://x.com/" + handle + "/status/" + q.RestID
	}
	qt.VideoURL, qt.VideoPoster, qt.VideoSecs = q.Legacy.video()
	qt.Images = q.Legacy.photos()
	return qt
}

// photos returns every attached still image, in the order x lists them. A video
// or GIF post carries no photo entity, so the two never both fill.
func (lg *legacyTweet) photos() []string {
	var out []string
	for _, m := range lg.ExtendedEntities.Media {
		if m.Type == "photo" && m.MediaURLHTTPS != "" {
			out = append(out, m.MediaURLHTTPS)
		}
	}
	return out
}

// video returns the best mp4 (highest bitrate) of the first attached video or
// animated GIF, its poster frame, and its length rounded to whole seconds; zero
// values when the post has none.
func (lg *legacyTweet) video() (url, poster string, secs int) {
	for _, m := range lg.ExtendedEntities.Media {
		if m.VideoInfo == nil || (m.Type != "video" && m.Type != "animated_gif") {
			continue
		}
		best := -1 // GIF variants carry bitrate 0, so start below it
		for _, v := range m.VideoInfo.Variants {
			if v.ContentType == "video/mp4" && v.Bitrate > best {
				best, url = v.Bitrate, v.URL
			}
		}
		if url != "" {
			return url, m.MediaURLHTTPS, (m.VideoInfo.DurationMillis + 500) / 1000
		}
	}
	return "", "", 0
}

// reTrailingTco strips the t.co link x.com appends for attached media or a
// quoted post; the card renders those itself, so the link is noise.
var reTrailingTco = regexp.MustCompile(`\s*https://t\.co/\w+\s*$`)

// cleanText renders a post body as text: entities decoded, every t.co the
// author actually wrote swapped back to the URL it points at, and only then the
// trailing shortlink dropped. Expanding first is what keeps a link the author
// ended their post with, since after expansion it is no longer a t.co and the
// only ones left are x's own for media and quotes.
func cleanText(s string, urls []urlEntity) string {
	s = expandLinks(html.UnescapeString(s), urls)
	for reTrailingTco.MatchString(s) {
		s = reTrailingTco.ReplaceAllString(s, "")
	}
	return strings.TrimSpace(s)
}

// expandLinks swaps the t.co shortlinks x rewrites into a body for the URLs
// they stand for, so a link reads as itself and points somewhere on its own.
func expandLinks(s string, urls []urlEntity) string {
	for _, u := range urls {
		if u.URL != "" && u.ExpandedURL != "" {
			s = strings.ReplaceAll(s, u.URL, u.ExpandedURL)
		}
	}
	return s
}

func relAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	}
}

type apiResponse struct {
	Data struct {
		Home struct {
			HomeTimelineURT struct {
				Instructions []instruction `json:"instructions"`
			} `json:"home_timeline_urt"`
		} `json:"home"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type instruction struct {
	Type        string       `json:"type"`
	Entries     []entry      `json:"entries"`
	ModuleItems []moduleItem `json:"moduleItems"` // TimelineAddToModule only
}

type entry struct {
	EntryID string `json:"entryId"`
	Content struct {
		EntryType   string       `json:"entryType"`
		ItemContent itemContent  `json:"itemContent"` // TimelineTimelineItem
		Items       []moduleItem `json:"items"`       // TimelineTimelineModule
	} `json:"content"`
}

// moduleItem is one post inside a module, e.g. a reply in a conversation the
// For You feed groups together.
type moduleItem struct {
	EntryID string `json:"entryId"`
	Item    struct {
		ItemContent itemContent `json:"itemContent"`
	} `json:"item"`
}

type itemContent struct {
	ItemType     string `json:"itemType"`
	TweetResults struct {
		Result *tweetResult `json:"result"`
	} `json:"tweet_results"`
}

type tweetResult struct {
	TypeName string       `json:"__typename"`
	Tweet    *tweetResult `json:"tweet"` // set when TypeName == TweetWithVisibilityResults
	RestID   string       `json:"rest_id"`
	Core     struct {
		UserResults struct {
			Result struct {
				Core struct {
					Name       string `json:"name"`
					ScreenName string `json:"screen_name"`
				} `json:"core"`
			} `json:"result"`
		} `json:"user_results"`
	} `json:"core"`
	NoteTweet *struct {
		NoteTweetResults struct {
			Result struct {
				Text      string `json:"text"`
				EntitySet struct {
					URLs []urlEntity `json:"urls"`
				} `json:"entity_set"`
			} `json:"result"`
		} `json:"note_tweet_results"`
	} `json:"note_tweet"`
	QuotedStatusResult *statusResult `json:"quoted_status_result"`
	Article            *struct {
		ArticleResults struct {
			Result *articleResult `json:"result"`
		} `json:"article_results"`
	} `json:"article"`
	Legacy *legacyTweet `json:"legacy"`
}

type statusResult struct {
	Result *tweetResult `json:"result"`
}

type articleResult struct {
	Title       string    `json:"title"`
	PreviewText string    `json:"preview_text"`
	CoverMedia  *apiMedia `json:"cover_media"`
	// the same image, wrapped, on the responses that spell it this way
	CoverMediaResults *struct {
		Result *apiMedia `json:"result"`
	} `json:"cover_media_results"`
	ContentState  articleContentState `json:"content_state"`
	MediaEntities []apiMedia          `json:"media_entities"`
}

// articleContentState is Draft.js: the body as a flat list of blocks.
type articleContentState struct {
	Blocks []struct {
		Text string `json:"text"`
		Type string `json:"type"` // unstyled, header-one…, ordered-list-item, atomic
	} `json:"blocks"`
}

type apiMedia struct {
	MediaInfo struct {
		OriginalImgURL string `json:"original_img_url"` // images only; a video has none
	} `json:"media_info"`
}

type legacyTweet struct {
	FullText              string        `json:"full_text"`
	CreatedAt             string        `json:"created_at"`
	ReplyCount            int           `json:"reply_count"`
	RetweetCount          int           `json:"retweet_count"`
	FavoriteCount         int           `json:"favorite_count"`
	QuoteCount            int           `json:"quote_count"`
	RetweetedStatusResult *statusResult `json:"retweeted_status_result"`
	QuotedStatusResult    *statusResult `json:"quoted_status_result"`
	// entities holds the links the author wrote; media and quote shortlinks are
	// not in here, which is what makes them safe to strip from the end.
	Entities struct {
		URLs []urlEntity `json:"urls"`
	} `json:"entities"`
	ExtendedEntities struct {
		Media []mediaEntity `json:"media"`
	} `json:"extended_entities"`
}

type urlEntity struct {
	URL         string `json:"url"` // the t.co shortlink as it appears in the body
	ExpandedURL string `json:"expanded_url"`
}

type mediaEntity struct {
	Type          string `json:"type"` // photo, video, animated_gif
	MediaURLHTTPS string `json:"media_url_https"`
	VideoInfo     *struct {
		DurationMillis int `json:"duration_millis"` // absent on some GIF entities
		Variants       []struct {
			Bitrate     int    `json:"bitrate"`
			ContentType string `json:"content_type"`
			URL         string `json:"url"`
		} `json:"variants"`
	} `json:"video_info"`
}
