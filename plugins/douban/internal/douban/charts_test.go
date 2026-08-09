package douban

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// chartFixture is the shape m.douban.com/rexxar/api/v2/subject_collection/
// movie_weekly_best/items answers with, trimmed to the fields the feed reads.
const chartFixture = `{
  "start": 0, "count": 20, "total": 2,
  "subject_collection_items": [
    {
      "id": "36808876",
      "title": "奥德赛",
      "card_subtitle": "2026 / 美国 英国 / 动作 历史 奇幻 冒险 / 克里斯托弗·诺兰 / 马特·达蒙 汤姆·霍兰德",
      "description": "改编自同名荷马史诗，讲述奥德修斯的故事，以及他在特洛伊战争后10年的回家之旅。",
      "cover_url": "https://img9.doubanio.com/view/photo/m_ratio_poster/public/p2933569626.jpg",
      "photos": ["https://img9.doubanio.com/view/photo/m/public/p2932615136.jpg"],
      "url": "https://movie.douban.com/subject/36808876/",
      "rank": 1, "trend_up": true, "trend_down": false,
      "rating": {"count": 68788, "max": 10, "star_count": 4.0, "value": 8.3}
    },
    {
      "id": "36990000",
      "title": "尚未上映",
      "card_subtitle": "2026 / 中国大陆 / 剧情",
      "description": "",
      "cover_url": "https://img9.doubanio.com/view/photo/m_ratio_poster/public/p2900000000.jpg",
      "photos": [],
      "url": "https://movie.douban.com/subject/36990000/",
      "rank": 2, "trend_up": false, "trend_down": true,
      "rating": null
    }
  ],
  "subject_collection": {
    "id": "movie_weekly_best",
    "name": "一周口碑电影榜",
    "title": "一周口碑电影榜",
    "updated_at": "2026-08-07 16:29:27"
  }
}`

func TestChartStatuses(t *testing.T) {
	var r chartResponse
	if err := json.Unmarshal([]byte(chartFixture), &r); err != nil {
		t.Fatalf("decoding chart: %v", err)
	}
	now := time.Date(2026, 8, 9, 8, 29, 27, 0, time.UTC)
	got := r.statuses(now)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}

	top := got[0]
	// namespaced so a subject can never collide with a status in the read store
	if top.ID != "chart:36808876" {
		t.Errorf("id = %q", top.ID)
	}
	if top.Author != "一周口碑电影榜" {
		t.Errorf("the chart is the entry's source: %q", top.Author)
	}
	// the headline carries rank, trend and name; the rating leads the body
	if top.Title != "#1 ↑ 奥德赛" {
		t.Errorf("headline = %q", top.Title)
	}
	if !strings.HasPrefix(top.Text, "8.3 ★ 68788人\n\n2026 / 美国 英国") {
		t.Errorf("body = %q", top.Text)
	}
	if !strings.Contains(top.Text, "改编自同名荷马史诗") {
		t.Errorf("the synopsis should ride in the body: %q", top.Text)
	}
	if top.URL != "https://movie.douban.com/subject/36808876/" {
		t.Errorf("url = %q", top.URL)
	}
	// poster first, then the stills
	want := []string{
		"https://img9.doubanio.com/view/photo/m_ratio_poster/public/p2933569626.jpg",
		"https://img9.doubanio.com/view/photo/m/public/p2932615136.jpg",
	}
	if len(top.Images) != 2 || top.Images[0] != want[0] || top.Images[1] != want[1] {
		t.Errorf("images = %v, want %v", top.Images, want)
	}
	// every title on one chart is dated by the chart, so the feed places them
	// where the list was published (16:29 CST == 08:29 UTC)
	if !top.CreatedAt.Equal(time.Date(2026, 8, 7, 8, 29, 27, 0, time.UTC)) {
		t.Errorf("createdAt = %v", top.CreatedAt)
	}
	if top.Age != "2d" {
		t.Errorf("age = %q", top.Age)
	}

	// an unrated title says nothing rather than 0.0, and a falling one marks it
	next := got[1]
	if next.Title != "#2 ↓ 尚未上映" {
		t.Errorf("headline = %q", next.Title)
	}
	if strings.Contains(next.Text, "0.0") || strings.Contains(next.Text, "★") {
		t.Errorf("no rating means no rating line: %q", next.Text)
	}
}

// A chart entry is the one feed row whose headline differs from its body: the
// card draws the rank and name above the rating and synopsis.
func TestToItemChartEntry(t *testing.T) {
	st := Status{
		ID:     ChartID("36808876"),
		Author: "一周口碑电影榜",
		Title:  "#1 ↑ 奥德赛",
		Text:   "8.3 ★ 68788人\n\n2026 / 美国 英国",
		URL:    "https://movie.douban.com/subject/36808876/",
		Age:    "2d",
	}
	it := ToItem(st)
	if it.Title != "#1 ↑ 奥德赛" {
		t.Errorf("title = %q", it.Title)
	}
	if it.Body != st.Text {
		t.Errorf("body = %q", it.Body)
	}
	// no activity to append, so the chart name stands alone as the source
	if it.Source != "一周口碑电影榜" {
		t.Errorf("source = %q", it.Source)
	}
}

func TestSortByRecency(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }
	statuses := []Status{
		{ID: "old", CreatedAt: day(1)},
		{ID: "undated"},
		{ID: "new", CreatedAt: day(9)},
		{ID: "chart-1", CreatedAt: day(7)},
		{ID: "chart-2", CreatedAt: day(7)},
	}
	sortByRecency(statuses)
	var order []string
	for _, s := range statuses {
		order = append(order, s.ID)
	}
	// newest first; one chart's entries keep their rank order; undated sinks
	want := []string{"new", "chart-1", "chart-2", "old", "undated"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
