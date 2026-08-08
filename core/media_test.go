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
