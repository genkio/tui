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
	for _, ins := range r.Data.Home.HomeTimelineURT.Instructions {
		if ins.Type != "TimelineAddEntries" {
			continue
		}
		for _, e := range ins.Entries {
			if strings.HasPrefix(e.EntryID, "promoted") {
				continue // skip ads
			}
			if e.Content.EntryType != "TimelineTimelineItem" || e.Content.ItemContent.ItemType != "TimelineTweet" {
				continue
			}
			res := e.Content.ItemContent.TweetResults.Result.normalize()
			if res == nil || res.Legacy == nil {
				continue // tombstone / unavailable post
			}
			tweets = append(tweets, res.toTweet())
		}
	}
	return tweets, nil
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

func (t *tweetResult) text() string {
	if t.NoteTweet != nil {
		if s := t.NoteTweet.NoteTweetResults.Result.Text; s != "" {
			return s // long-form post; legacy.full_text would be truncated
		}
	}
	if t.Legacy != nil {
		return t.Legacy.FullText
	}
	return ""
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
	tw.VideoURL, tw.VideoPoster = lg.video()
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
	a := &Article{Title: cleanText(res.Title), Cover: res.cover()}
	if body := res.ContentState.text(); body != "" {
		a.Text = body
	} else if p := cleanText(res.PreviewText); p != "" {
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
	qt.VideoURL, qt.VideoPoster = q.Legacy.video()
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
// animated GIF, plus its poster frame; empty strings when the post has none.
func (lg *legacyTweet) video() (url, poster string) {
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
			return url, m.MediaURLHTTPS
		}
	}
	return "", ""
}

// reTrailingTco strips the t.co link x.com appends for attached media or a
// quoted post; it is noise in a text-only view.
var reTrailingTco = regexp.MustCompile(`\s*https://t\.co/\w+\s*$`)

func cleanText(s string) string {
	s = html.UnescapeString(s)
	for reTrailingTco.MatchString(s) {
		s = reTrailingTco.ReplaceAllString(s, "")
	}
	return strings.TrimSpace(s)
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
	Type    string  `json:"type"`
	Entries []entry `json:"entries"`
}

type entry struct {
	EntryID string `json:"entryId"`
	Content struct {
		EntryType   string `json:"entryType"`
		ItemContent struct {
			ItemType     string `json:"itemType"`
			TweetResults struct {
				Result *tweetResult `json:"result"`
			} `json:"tweet_results"`
		} `json:"itemContent"`
	} `json:"content"`
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
				Text string `json:"text"`
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
	ExtendedEntities      struct {
		Media []mediaEntity `json:"media"`
	} `json:"extended_entities"`
}

type mediaEntity struct {
	Type          string `json:"type"` // photo, video, animated_gif
	MediaURLHTTPS string `json:"media_url_https"`
	VideoInfo     *struct {
		Variants []struct {
			Bitrate     int    `json:"bitrate"`
			ContentType string `json:"content_type"`
			URL         string `json:"url"`
		} `json:"variants"`
	} `json:"video_info"`
}
