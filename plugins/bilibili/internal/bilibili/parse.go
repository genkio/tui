package bilibili

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// feedPage is one decoded page of the 动态 feed: its videos plus what the next
// request needs to continue, and the reply's own status so the caller can tell a
// dead session from an empty timeline.
type feedPage struct {
	Code    int
	Message string
	Videos  []Video
	Offset  string
	HasMore bool
}

// feedReply mirrors the parts of the web-dynamic reply this app reads. The feed
// carries a dozen dynamic kinds; everything but a video post is skipped, so the
// unread parts of the shape are simply left undeclared.
type feedReply struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		HasMore bool       `json:"has_more"`
		Offset  string     `json:"offset"`
		Items   []feedItem `json:"items"`
	} `json:"data"`
}

// scalar is a JSON value bilibili states inconsistently: a bare number in one
// dynamic and the same value quoted in the next (pub_ts and the play count both
// come either way). It is kept as text and read as a number where one is wanted,
// so a quoted timestamp cannot fail the whole feed.
type scalar string

func (s *scalar) UnmarshalJSON(b []byte) error {
	raw := strings.TrimSpace(string(b))
	if raw == "null" {
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*s = scalar(str)
		return nil
	}
	*s = scalar(raw)
	return nil
}

func (s scalar) text() string { return strings.TrimSpace(string(s)) }

func (s scalar) num() int64 {
	n, _ := strconv.ParseInt(s.text(), 10, 64)
	return n
}

type feedItem struct {
	IDStr   string `json:"id_str"`
	Type    string `json:"type"`
	Visible bool   `json:"visible"`
	Modules struct {
		Author struct {
			Name  string `json:"name"`
			PubTS scalar `json:"pub_ts"`
		} `json:"module_author"`
		Dynamic struct {
			Desc *struct {
				Text string `json:"text"`
			} `json:"desc"`
			Major *struct {
				Type    string    `json:"type"`
				Archive *majorVid `json:"archive"`
				PGC     *majorVid `json:"pgc"`
			} `json:"major"`
		} `json:"module_dynamic"`
	} `json:"modules"`
}

// majorVid covers both video majors: an uploader's own submission (archive) and
// an episode of a series (pgc). The two agree on every field this app reads,
// except that a series episode has no bvid and states no running time.
type majorVid struct {
	BVID         string `json:"bvid"`
	Title        string `json:"title"`
	Desc         string `json:"desc"`
	Cover        string `json:"cover"`
	DurationText string `json:"duration_text"`
	JumpURL      string `json:"jump_url"`
	Badge        struct {
		Text string `json:"text"`
	} `json:"badge"`
	Stat struct {
		Play scalar `json:"play"`
	} `json:"stat"`
}

// parseFeed decodes one page of the following 动态 into videos, newest first as
// bilibili sends them. now is threaded in (not time.Now) so a whole fetch shares
// one clock and tests are deterministic.
func parseFeed(raw []byte, now time.Time) (feedPage, error) {
	var reply feedReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return feedPage{}, fmt.Errorf("decoding bilibili 动态 feed: %w", err)
	}
	page := feedPage{
		Code:    reply.Code,
		Message: reply.Message,
		Offset:  reply.Data.Offset,
		HasMore: reply.Data.HasMore,
	}
	for _, it := range reply.Data.Items {
		if v, ok := toVideo(it, now); ok {
			page.Videos = append(page.Videos, v)
		}
	}
	return page, nil
}

// toVideo pulls a video post out of a dynamic, reporting false for everything
// else: a text status, a picture album, a live-stream nudge, or a post whose
// content this session is not allowed to see.
func toVideo(it feedItem, now time.Time) (Video, bool) {
	major := it.Modules.Dynamic.Major
	if it.IDStr == "" || major == nil {
		return Video{}, false
	}
	src := major.Archive
	if src == nil {
		src = major.PGC
	}
	if src == nil || src.Title == "" {
		return Video{}, false
	}
	// A dynamic the session cannot open (members-only, deleted, region-locked)
	// still arrives, with its content blanked out.
	if !it.Visible && src.JumpURL == "" {
		return Video{}, false
	}

	v := Video{
		ID:     it.IDStr,
		BVID:   src.BVID,
		Title:  strings.TrimSpace(src.Title),
		Desc:   cleanDesc(src.Desc),
		Author: strings.TrimSpace(it.Modules.Author.Name),
		URL:    watchURL(src.JumpURL, src.BVID),
		Cover:  mediaURL(src.Cover),
		Secs:   durationSecs(src.DurationText),
		Views:  src.Stat.Play.text(),
		Badge:  badgeText(src.Badge.Text),
	}
	if desc := it.Modules.Dynamic.Desc; desc != nil {
		v.Note = cleanDesc(desc.Text)
	}
	if v.Note == v.Desc {
		v.Note = "" // the note is the description repeated; keep one copy
	}
	if ts := it.Modules.Author.PubTS.num(); ts > 0 {
		v.PubAt = time.Unix(ts, 0).UTC()
		v.Age = relAge(now.Sub(v.PubAt))
	}
	return v, true
}

// genericBadges say only "this is a video", which nearly every submission on the
// 动态 carries. Prefixing 38 of 40 titles with a word that distinguishes nothing
// is worse than no badge, so only the ones that mean something survive (番剧,
// 合作视频, 抢先看, …).
var genericBadges = map[string]bool{"投稿视频": true, "动态视频": true, "视频": true, "投稿": true}

func badgeText(raw string) string {
	s := strings.TrimSpace(raw)
	if genericBadges[s] {
		return ""
	}
	return s
}

// cleanDesc tidies a description for a one-line row: bilibili pads them with
// blank lines, and "-" is what its uploader form leaves when nothing was typed.
func cleanDesc(s string) string {
	s = strings.TrimSpace(s)
	if s == "-" {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// watchURL normalizes the protocol-relative address the feed states, falling
// back to the canonical video page when a dynamic states none.
func watchURL(jump, bvid string) string {
	if u := mediaURL(jump); u != "" {
		return u
	}
	if bvid != "" {
		return "https://www.bilibili.com/video/" + bvid
	}
	return ""
}

// mediaURL upgrades bilibili's protocol-relative and plain-http asset addresses
// to https: the web page is served over https from a phone, which will not load
// a cover over http.
func mediaURL(raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "//"):
		return "https:" + s
	case strings.HasPrefix(s, "http://"):
		return "https://" + strings.TrimPrefix(s, "http://")
	case strings.HasPrefix(s, "https://"):
		return s
	}
	return ""
}

// durationSecs reads the running time the feed states as "12:34" or "1:02:33".
// Anything else reports 0, which the card takes as "length unknown" and draws no
// badge for.
func durationSecs(text string) int {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	secs := 0
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 {
			return 0
		}
		secs = secs*60 + n
	}
	return secs
}

// relAge renders a publish time as the compact relative age the feed rows show.
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
