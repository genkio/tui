package reddit

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseHome(t *testing.T) {
	body := `{
	  "kind": "Listing",
	  "data": {
	    "children": [
	      {"kind": "t3", "data": {
	        "id": "1a2b3", "subreddit": "golang",
	        "title": "Go 1.26 released", "selftext": "It ships.",
	        "is_self": true, "author": "alice",
	        "permalink": "/r/golang/comments/1a2b3/go_126_released/",
	        "url": "https://www.reddit.com/r/golang/comments/1a2b3/go_126_released/",
	        "created_utc": 1700000000
	      }},
	      {"kind": "t3", "data": {
	        "id": "x9y8", "subreddit": "programming",
	        "title": "Why are we still using C", "selftext": "",
	        "is_self": false, "author": "bob",
	        "permalink": "/r/programming/comments/x9y8/why_are_we_still_using_c/",
	        "url": "https://example.com/c-article",
	        "created_utc": 1699999000
	      }},
	      {"kind": "t5", "data": {"id": "zzz"}}
	    ]
	  }
	}`

	posts, err := parseHome([]byte(body))
	if err != nil {
		t.Fatalf("parseHome: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}

	p := posts[0]
	if p.ID != "1a2b3" || p.Subreddit != "golang" || !p.IsSelf {
		t.Errorf("self post fields wrong: %+v", p)
	}
	if !p.CreatedAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("createdAt wrong: %v", p.CreatedAt)
	}
	if p.Age == "" {
		t.Errorf("age not derived: %+v", p)
	}

	link := posts[1]
	if link.IsSelf || link.SelfText != "" {
		t.Errorf("link post fields wrong: %+v", link)
	}
}

func TestToItemURL(t *testing.T) {
	self := Post{ID: "1", IsSelf: true, Permalink: "/r/golang/comments/1/t/", URL: "https://www.reddit.com/r/golang/comments/1/t/"}
	if got := ToItem(self).URL; got != "https://old.reddit.com/r/golang/comments/1/t/" {
		t.Errorf("self post: want old.reddit thread, got %q", got)
	}

	external := Post{ID: "2", IsSelf: false, Permalink: "/r/programming/comments/2/t/", URL: "https://example.com/article"}
	if got := ToItem(external).URL; got != "https://example.com/article" {
		t.Errorf("link post: want external url, got %q", got)
	}

	image := Post{ID: "3", IsSelf: false, Permalink: "/r/pics/comments/3/t/", URL: "https://i.redd.it/abc.jpg"}
	if got := ToItem(image).URL; got != "https://old.reddit.com/r/pics/comments/3/t/" {
		t.Errorf("image post: want thread, got %q", got)
	}
}

func TestParseHomeFloatCreatedUTC(t *testing.T) {
	// Reddit sends created_utc as a float (1786017121.0); the parser must accept it.
	body := `{"kind":"Listing","data":{"children":[{"kind":"t3","data":{
	  "id":"1a2b3","subreddit":"golang","title":"t","is_self":false,
	  "created_utc":1786017121.0}}]}}`
	posts, err := parseHome([]byte(body))
	if err != nil {
		t.Fatalf("parseHome with float created_utc: %v", err)
	}
	if len(posts) != 1 || !posts[0].CreatedAt.Equal(time.Unix(1786017121, 0).UTC()) {
		t.Errorf("bad parse: %+v err=%v", posts, err)
	}
}

func TestParseHomeImages(t *testing.T) {
	// A gallery: media_metadata is a map, so gallery_data carries the only
	// ordering. The ids are listed out of map order on purpose.
	gallery := `{"kind":"t3","data":{"id":"g1","subreddit":"pics","title":"album",
	  "gallery_data":{"items":[{"media_id":"bbb"},{"media_id":"aaa"}]},
	  "media_metadata":{
	    "aaa":{"s":{"u":"https://preview.redd.it/aaa.jpg?width=1080&s=sig"}},
	    "bbb":{"s":{"u":"https://preview.redd.it/bbb.jpg?width=1080&s=sig"}}}}}`
	// A direct image link wins over the preview reddit also renders for it.
	direct := `{"kind":"t3","data":{"id":"d1","subreddit":"pics","title":"shot",
	  "url":"https://i.redd.it/abc.png",
	  "preview":{"images":[{"source":{"url":"https://preview.redd.it/abc.png?s=sig"}}]}}}`
	// A link post has no image of its own, so reddit's preview stands in.
	link := `{"kind":"t3","data":{"id":"l1","subreddit":"programming","title":"article",
	  "url":"https://example.com/post",
	  "preview":{"images":[{"source":{"url":"https://external-preview.redd.it/xyz.jpg?s=sig"}}]}}}`
	text := `{"kind":"t3","data":{"id":"t1","subreddit":"golang","title":"words","is_self":true,
	  "url":"https://www.reddit.com/r/golang/comments/t1/words/"}}`

	body := `{"kind":"Listing","data":{"children":[` + gallery + `,` + direct + `,` + link + `,` + text + `]}}`
	posts, err := parseHome([]byte(body))
	if err != nil {
		t.Fatalf("parseHome: %v", err)
	}
	if len(posts) != 4 {
		t.Fatalf("got %d posts, want 4", len(posts))
	}

	want := [][]string{
		{"https://preview.redd.it/bbb.jpg?width=1080&s=sig", "https://preview.redd.it/aaa.jpg?width=1080&s=sig"},
		{"https://i.redd.it/abc.png"},
		{"https://external-preview.redd.it/xyz.jpg?s=sig"},
		nil,
	}
	for i, w := range want {
		got := posts[i].Images
		if len(got) != len(w) {
			t.Errorf("post %s images = %v, want %v", posts[i].ID, got, w)
			continue
		}
		for j := range w {
			if got[j] != w[j] {
				t.Errorf("post %s images = %v, want %v", posts[i].ID, got, w)
				break
			}
		}
	}
	// The images reach the shared item shape the web card reads.
	if got := ToItem(posts[1]).Images; len(got) != 1 || got[0] != "https://i.redd.it/abc.png" {
		t.Errorf("ToItem dropped images: %v", got)
	}
}

func TestIsImageURL(t *testing.T) {
	for _, u := range []string{
		"https://i.redd.it/a.jpg", "https://i.redd.it/a.JPEG",
		"https://x.com/a.png", "https://x.com/a.gif", "https://x.com/a.webp",
		"https://i.redd.it/a.jpg?width=1", "https://i.redd.it/a.png#frag",
	} {
		if !isImageURL(u) {
			t.Errorf("isImageURL(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"", "https://example.com/post", "https://example.com/a.jpg.html",
		"https://v.redd.it/abc", "https://example.com/gallery?img=a.jpg",
	} {
		if isImageURL(u) {
			t.Errorf("isImageURL(%q) = true, want false", u)
		}
	}
}

func TestRelAge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{2 * 24 * time.Hour, "2d"},
		{3 * 7 * 24 * time.Hour, "3w"},
	}
	for _, c := range cases {
		if got := relAge(c.d); got != c.want {
			t.Errorf("relAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
	_ = now
}

// ensure the parse types round-trip the shape the API sends.
func TestListingJSONShape(t *testing.T) {
	raw := []byte(`{"kind":"Listing","data":{"children":[{"kind":"t3","data":{"id":"abc","created_utc":1700000000}}]}}`)
	var l listing
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(l.Data.Children) != 1 || l.Data.Children[0].Data.ID != "abc" {
		t.Errorf("listing shape wrong: %+v", l)
	}
}
