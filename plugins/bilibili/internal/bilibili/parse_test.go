package bilibili

import (
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

const feedJSON = `{
  "code": 0,
  "message": "0",
  "data": {
    "has_more": true,
    "offset": "1039876543210",
    "items": [
      {
        "id_str": "1000000000000000001",
        "type": "DYNAMIC_TYPE_AV",
        "visible": true,
        "modules": {
          "module_author": {"name": "老师好我叫何同学", "pub_ts": 1772280000},
          "module_dynamic": {
            "desc": {"text": "花了三个月做这个"},
            "major": {
              "type": "MAJOR_TYPE_ARCHIVE",
              "archive": {
                "bvid": "BV1GJ411x7h7",
                "title": "我做了一个东西",
                "desc": "花了三个月做这个\n\n关注我",
                "cover": "http://i2.hdslb.com/bfs/archive/cover.jpg",
                "duration_text": "12:34",
                "jump_url": "//www.bilibili.com/video/BV1GJ411x7h7",
                "badge": {"text": ""},
                "stat": {"play": "1.2万"}
              }
            }
          }
        }
      },
      {
        "id_str": "1000000000000000002",
        "type": "DYNAMIC_TYPE_PGC",
        "visible": true,
        "modules": {
          "module_author": {"name": "某某番剧", "pub_ts": 1772352000},
          "module_dynamic": {
            "major": {
              "type": "MAJOR_TYPE_PGC",
              "pgc": {
                "title": "第 3 话 出发",
                "desc": "-",
                "cover": "https://i0.hdslb.com/bfs/bangumi/ep.jpg",
                "jump_url": "//www.bilibili.com/bangumi/play/ep123456",
                "badge": {"text": "番剧"},
                "stat": {"play": "300万"}
              }
            }
          }
        }
      },
      {
        "id_str": "1000000000000000003",
        "type": "DYNAMIC_TYPE_WORD",
        "visible": true,
        "modules": {
          "module_author": {"name": "写字的人", "pub_ts": 1772352000},
          "module_dynamic": {"desc": {"text": "今天天气不错"}}
        }
      }
    ]
  }
}`

func TestParseFeed(t *testing.T) {
	page, err := parseFeed([]byte(feedJSON), testNow)
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if page.Code != 0 || !page.HasMore || page.Offset != "1039876543210" {
		t.Fatalf("unexpected page envelope: %+v", page)
	}
	// The text status carries no video, so it is not one of ours.
	if len(page.Videos) != 2 {
		t.Fatalf("want 2 video posts, got %d: %+v", len(page.Videos), page.Videos)
	}

	got := page.Videos[0]
	want := Video{
		ID:     "1000000000000000001",
		BVID:   "BV1GJ411x7h7",
		Title:  "我做了一个东西",
		Note:   "花了三个月做这个",
		Desc:   "花了三个月做这个\n关注我",
		Author: "老师好我叫何同学",
		URL:    "https://www.bilibili.com/video/BV1GJ411x7h7",
		Cover:  "https://i2.hdslb.com/bfs/archive/cover.jpg",
		Secs:   754,
		Views:  "1.2万",
		PubAt:  time.Unix(1772280000, 0).UTC(),
		Age:    "1d",
	}
	// The note and the description both survive; the card shows the note, which
	// is what the uploader chose to say when posting it.
	if got != want {
		t.Errorf("archive video:\n got %+v\nwant %+v", got, want)
	}

	ep := page.Videos[1]
	if ep.Badge != "番剧" || ep.BVID != "" || ep.Secs != 0 {
		t.Errorf("series episode: want a badged, bvid-less, lengthless post, got %+v", ep)
	}
	if ep.Desc != "" {
		t.Errorf(`the "-" placeholder description should be dropped, got %q`, ep.Desc)
	}
	if ep.URL != "https://www.bilibili.com/bangumi/play/ep123456" {
		t.Errorf("series episode url = %q", ep.URL)
	}
}

func TestParseFeedKeepsTheUploadersNote(t *testing.T) {
	raw := `{"code":0,"data":{"items":[{"id_str":"7","type":"DYNAMIC_TYPE_AV","visible":true,"modules":{
	  "module_author":{"name":"up","pub_ts":1772366400},
	  "module_dynamic":{"desc":{"text":"顺便说一句"},"major":{"archive":{"bvid":"BV1GJ411x7h7","title":"标题","desc":"视频简介"}}}}}]}}`
	page, err := parseFeed([]byte(raw), testNow)
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	v := page.Videos[0]
	if v.Note != "顺便说一句" || v.Desc != "视频简介" {
		t.Fatalf("want both the note and the description, got note=%q desc=%q", v.Note, v.Desc)
	}
	if body := v.body(); body != "顺便说一句" {
		t.Errorf("the note is what the uploader wanted said, got %q", body)
	}
}

func TestParseFeedReportsTheReplyCode(t *testing.T) {
	page, err := parseFeed([]byte(`{"code":-101,"message":"账号未登录"}`), testNow)
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if page.Code != -101 || len(page.Videos) != 0 {
		t.Fatalf("want an empty page carrying code -101, got %+v", page)
	}
	if err := apiError(page.Code, page.Message); err == nil {
		t.Fatal("a logged-out reply should be an error")
	} else if got := err.Error(); !strings.Contains(got, "session is stale") {
		t.Errorf("want re-auth advice, got %q", got)
	}
}

func TestParseFeedSkipsInvisiblePosts(t *testing.T) {
	raw := `{"code":0,"data":{"items":[{"id_str":"9","type":"DYNAMIC_TYPE_AV","visible":false,"modules":{
	  "module_author":{"name":"up"},"module_dynamic":{"major":{"archive":{"title":"充电专属"}}}}}]}}`
	page, err := parseFeed([]byte(raw), testNow)
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(page.Videos) != 0 {
		t.Fatalf("a post this session cannot open should be skipped, got %+v", page.Videos)
	}
}

func TestDurationSecs(t *testing.T) {
	for in, want := range map[string]int{
		"0:42":    42,
		"12:34":   754,
		"1:02:33": 3753,
		" 05:00 ": 300,
		"":        0,
		"42":      0,
		"1:2:3:4": 0,
		"直播中":     0,
	} {
		if got := durationSecs(in); got != want {
			t.Errorf("durationSecs(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestMediaURL(t *testing.T) {
	for in, want := range map[string]string{
		"//i0.hdslb.com/a.jpg":      "https://i0.hdslb.com/a.jpg",
		"http://i0.hdslb.com/a.jpg": "https://i0.hdslb.com/a.jpg",
		"https://i0.hdslb.com/a":    "https://i0.hdslb.com/a",
		"":                          "",
		"data:image/png;base64,xx":  "",
	} {
		if got := mediaURL(in); got != want {
			t.Errorf("mediaURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToItem(t *testing.T) {
	v := Video{
		ID:     "42",
		BVID:   "BV1GJ411x7h7",
		Title:  "标题",
		Desc:   "简介",
		Author: "up主",
		Views:  "1.2万",
		URL:    "https://www.bilibili.com/video/BV1GJ411x7h7",
		Cover:  "https://i0.hdslb.com/cover.jpg",
		Secs:   754,
		Age:    "2h",
		PubAt:  testNow.Add(-2 * time.Hour),
	}
	it := ToItem(v)
	if it.App != "bilibili" || it.ID != "42" || it.Title != "标题" || it.Body != "简介" {
		t.Fatalf("unexpected item: %+v", it)
	}
	if it.Source != "up主 · 1.2万播放" {
		t.Errorf("source should carry who posted it and how watched it is, got %q", it.Source)
	}
	if it.Author != "" {
		t.Errorf("the author is already in the source; Author would repeat it, got %q", it.Author)
	}
	if it.Poster != v.Cover || it.VidSecs != 754 {
		t.Errorf("the still and the running time should travel with the item: %+v", it)
	}
	if it.Video != "" {
		t.Errorf("bilibili hands out no stream a page can use, got %q", it.Video)
	}
	if !it.At.Equal(v.PubAt.UTC()) {
		t.Errorf("At = %v, want %v", it.At, v.PubAt.UTC())
	}

	badged := ToItem(Video{ID: "1", Title: "第 3 话", Badge: "番剧"})
	if badged.Title != "[番剧] 第 3 话" {
		t.Errorf("a series episode should say so in its title, got %q", badged.Title)
	}
}

// bilibili states pub_ts and the play count as a bare number in one dynamic and
// quoted in the next; either has to decode, since one odd value used to fail the
// whole page.
func TestParseFeedTakesQuotedScalars(t *testing.T) {
	raw := `{"code":0,"data":{"items":[
	  {"id_str":"1","type":"DYNAMIC_TYPE_AV","visible":true,"modules":{
	    "module_author":{"name":"up","pub_ts":"1772280000"},
	    "module_dynamic":{"major":{"archive":{"bvid":"BV1GJ411x7h7","title":"引号","stat":{"play":12345}}}}}},
	  {"id_str":"2","type":"DYNAMIC_TYPE_AV","visible":true,"modules":{
	    "module_author":{"name":"up","pub_ts":1772280000},
	    "module_dynamic":{"major":{"archive":{"bvid":"BV1GJ411x7h8","title":"数字","stat":{"play":"1.2万"}}}}}},
	  {"id_str":"3","type":"DYNAMIC_TYPE_AV","visible":true,"modules":{
	    "module_author":{"name":"up","pub_ts":null},
	    "module_dynamic":{"major":{"archive":{"bvid":"BV1GJ411x7h9","title":"没时间"}}}}}
	]}}`
	page, err := parseFeed([]byte(raw), testNow)
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(page.Videos) != 3 {
		t.Fatalf("want all three posts, got %d", len(page.Videos))
	}
	quoted, plain, undated := page.Videos[0], page.Videos[1], page.Videos[2]
	want := time.Unix(1772280000, 0).UTC()
	if !quoted.PubAt.Equal(want) || !plain.PubAt.Equal(want) {
		t.Errorf("both spellings of pub_ts should date the post: %v vs %v", quoted.PubAt, plain.PubAt)
	}
	if quoted.Age != "1d" || plain.Age != "1d" {
		t.Errorf("ages = %q and %q, want 1d", quoted.Age, plain.Age)
	}
	if quoted.Views != "12345" || plain.Views != "1.2万" {
		t.Errorf("views = %q and %q, want them read either way", quoted.Views, plain.Views)
	}
	// No timestamp is not a broken post: it sorts to the bottom of the merged feed.
	if !undated.PubAt.IsZero() || undated.Age != "" {
		t.Errorf("a post with no pub_ts should carry no time: %+v", undated)
	}
}

func TestBadgeText(t *testing.T) {
	// The badge is there to mark what stands out; a word 38 of 40 posts carry is
	// not that.
	for in, want := range map[string]string{
		"投稿视频": "",
		"动态视频": "",
		" 番剧 ": "番剧",
		"合作视频": "合作视频",
		"抢先看":  "抢先看",
		"":     "",
	} {
		if got := badgeText(in); got != want {
			t.Errorf("badgeText(%q) = %q, want %q", in, got, want)
		}
	}
}
