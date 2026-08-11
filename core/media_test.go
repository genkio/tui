package core

import (
	"strconv"
	"strings"
	"testing"
)

func TestImagesFromHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"plain img", `<p>hi</p><img src="https://e.com/a.jpg">`, []string{"https://e.com/a.jpg"}},
		{
			// A lazy feed ships a placeholder in src and the real file in data-src.
			"lazy wins over placeholder",
			`<img src="https://e.com/blank.gif" data-src="https://e.com/real.jpg">`,
			[]string{"https://e.com/real.jpg"},
		},
		{"data-original", `<img data-original="https://e.com/o.png">`, []string{"https://e.com/o.png"}},
		{"protocol relative", `<img src="//e.com/p.jpg">`, []string{"https://e.com/p.jpg"}},
		{"tracking beacon dropped", `<img src="https://e.com/px.gif" width="1" height="1">`, nil},
		{"zero-size dropped", `<img src="https://e.com/px.gif" height="0">`, nil},
		{"data uri dropped", `<img src="data:image/png;base64,AAAA">`, nil},
		{"relative path dropped", `<img src="/media/x.jpg">`, nil},
		{"empty src dropped", `<img src="">`, nil},
		{"deduped", `<img src="https://e.com/a.jpg"><img src="https://e.com/a.jpg">`, []string{"https://e.com/a.jpg"}},
		{
			"document order kept",
			`<img src="https://e.com/1.jpg"><p>x</p><div><img src="https://e.com/2.jpg"></div>`,
			[]string{"https://e.com/1.jpg", "https://e.com/2.jpg"},
		},
		{"no images", `<p>just words</p>`, nil},
		{"empty", "", nil},
	}
	for _, c := range cases {
		got := ImagesFromHTML(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

// A long article body must not drag its whole image gallery into the feed.
func TestImagesFromHTMLCaps(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxHTMLImages+10; i++ {
		b.WriteString(`<img src="https://e.com/` + strconv.Itoa(i) + `.jpg">`)
	}
	if got := ImagesFromHTML(b.String()); len(got) != maxHTMLImages {
		t.Errorf("got %d images, want the cap of %d", len(got), maxHTMLImages)
	}
}

func TestAudioFromHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"audio src", `<audio preload="metadata" src="https://e.com/ep.mp3"></audio>`, "https://e.com/ep.mp3"},
		{
			"source child",
			`<audio controls><source src="https://e.com/ep.m4a" type="audio/mp4"></audio>`,
			"https://e.com/ep.m4a",
		},
		{
			// Inoreader parks the enclosure here for the player its own script
			// builds; the class is what says the file is audio.
			"data-media-url on the player div",
			`<div class="enclosures_audio_player audio-url" data-media-url="https://dts.podtrac.com/redirect.mp3/x/default.mp3?aid=rss_feed" data-media-type="0"></div>`,
			"https://dts.podtrac.com/redirect.mp3/x/default.mp3?aid=rss_feed",
		},
		{
			"typed enclosure link",
			`<a href="https://e.com/dl?id=7" data-type="audio/mpeg">default.mp3</a>`,
			"https://e.com/dl?id=7",
		},
		{"link by extension", `<a href="https://e.com/ep.mp3?utm=1">ep.mp3</a>`, "https://e.com/ep.mp3?utm=1"},
		{
			"player wins over the link below it",
			`<audio src="https://e.com/ep.mp3"></audio><a href="https://e.com/other.mp3">other</a>`,
			"https://e.com/ep.mp3",
		},
		{"a video carrier is not audio", `<div data-media-url="https://e.com/clip.mp4"></div>`, ""},
		{"plain link ignored", `<a href="https://e.com/post">post</a>`, ""},
		{"no media", `<p>just words</p>`, ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := AudioFromHTML(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestImageURL(t *testing.T) {
	cases := map[string]string{
		"https://e.com/a.jpg":   "https://e.com/a.jpg",
		"http://e.com/a.jpg":    "http://e.com/a.jpg",
		"//e.com/a.jpg":         "https://e.com/a.jpg",
		"  https://e.com/a.jpg": "https://e.com/a.jpg",
		"/rel/a.jpg":            "",
		"data:image/png;x":      "",
		"":                      "",
	}
	for in, want := range cases {
		if got := ImageURL(in); got != want {
			t.Errorf("ImageURL(%q) = %q, want %q", in, got, want)
		}
	}
}
