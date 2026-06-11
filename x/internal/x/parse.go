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
	if lg.QuotedStatusResult != nil && lg.QuotedStatusResult.Result != nil {
		if q := lg.QuotedStatusResult.Result.normalize(); q != nil && q.Legacy != nil {
			qn, qh := q.author()
			tw.Quoted = &QuotedTweet{Name: qn, Handle: qh, Text: cleanText(q.text())}
		}
	}
	return tw
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
	Legacy *legacyTweet `json:"legacy"`
}

type legacyTweet struct {
	FullText              string `json:"full_text"`
	CreatedAt             string `json:"created_at"`
	ReplyCount            int    `json:"reply_count"`
	RetweetCount          int    `json:"retweet_count"`
	FavoriteCount         int    `json:"favorite_count"`
	QuoteCount            int    `json:"quote_count"`
	RetweetedStatusResult *struct {
		Result *tweetResult `json:"result"`
	} `json:"retweeted_status_result"`
	QuotedStatusResult *struct {
		Result *tweetResult `json:"result"`
	} `json:"quoted_status_result"`
}
