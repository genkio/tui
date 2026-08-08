package core

import (
	"strings"

	"golang.org/x/net/html"
)

// maxHTMLImages bounds what one article contributes. A long feed body can carry
// dozens of inline images; the card only needs enough to see what it is about.
const maxHTMLImages = 8

// ImagesFromHTML pulls the content images out of a feed body, in document
// order, deduped. Feeds that lazy-load leave a placeholder in src and the real
// URL in data-src, so that one wins when both are set.
func ImagesFromHTML(fragment string) []string {
	if !strings.Contains(fragment, "<img") {
		return nil
	}
	doc, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			if u := imgURL(n); u != "" && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(out) > maxHTMLImages {
		out = out[:maxHTMLImages]
	}
	return out
}

// imgURL resolves one <img> to a usable URL, or "" when it carries none or is a
// 1x1 tracking beacon rather than content.
func imgURL(n *html.Node) string {
	var src, lazy string
	for _, a := range n.Attr {
		switch a.Key {
		case "src":
			src = a.Val
		case "data-src", "data-original", "data-lazy-src":
			if lazy == "" {
				lazy = a.Val
			}
		case "width", "height":
			if a.Val == "0" || a.Val == "1" {
				return ""
			}
		}
	}
	if u := ImageURL(lazy); u != "" {
		return u
	}
	return ImageURL(src)
}

// ImageURL normalizes one image reference to something a browser can load from
// the feed page: an absolute http(s) URL. A data: URI, a site-relative path
// (unresolvable without the article's base), or an empty attribute yields "".
func ImageURL(raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "//"): // protocol-relative
		return "https:" + s
	case strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "http://"):
		return s
	}
	return ""
}
