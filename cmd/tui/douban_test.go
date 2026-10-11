package main

import (
	"strings"
	"testing"

	"github.com/genkio/tui/core"
)

func TestDoubanAppURL(t *testing.T) {
	for _, tc := range []struct {
		app, url string
		want     string
	}{
		{"douban", "https://www.douban.com/people/270860254/status/9843290158/", "douban://douban.com/status/9843290158"},
		{"douban", "https://www.douban.com/topic/502187006/", "douban://douban.com/topic/502187006"},
		{"douban", "https://www.douban.com/group/topic/497864000/", "douban://douban.com/group/topic/497864000"},
		{"douban", "https://www.douban.com/photos/photo/2935000000/", "douban://douban.com/photo/2935000000"},
		{"douban", "https://www.douban.com/podcast_episode/500000", "douban://douban.com/podcast/episode/500000"},
		{"douban", "https://movie.douban.com/review/17802242/", "douban://douban.com/review/17802242"},
		{"douban", "https://movie.douban.com/subject/27190135/", "douban://douban.com/movie/27190135"},
		{"douban", "https://book.douban.com/subject/1084336/", "douban://douban.com/book/1084336"},
		{"douban", "https://www.douban.com/", ""},                 // no shape the app has a page for
		{"reddit", "https://www.douban.com/topic/502187006/", ""}, // only douban's own items
	} {
		if got := string(doubanAppURL(tc.app, tc.url)); got != tc.want {
			t.Errorf("doubanAppURL(%q, %q) = %q, want %q", tc.app, tc.url, got, tc.want)
		}
	}
}

func TestDoubanCardRendersAppLink(t *testing.T) {
	html := renderCard(t, core.Item{App: "douban", ID: "1", URL: "https://www.douban.com/topic/502187006/", Title: "t"})
	if !strings.Contains(html, `href="douban://douban.com/topic/502187006"`) {
		t.Errorf("card has no douban app link: %s", html)
	}
}
