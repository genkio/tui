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

// AudioFromHTML pulls the audio enclosure out of a feed body: the episode file
// a podcast item attaches. Readers express it in more than one way — a real
// <audio> element, an element carrying the URL for a player their own script
// builds later, or just a link in an enclosures table — so all three are read,
// in that order of trust, and the first hit wins.
func AudioFromHTML(fragment string) string {
	doc, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return ""
	}
	var player, carrier, link string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "audio":
				if player == "" {
					player = mediaSrc(n)
				}
			case "a":
				if link == "" && audioLink(n) {
					link = mediaURL(nodeAttr(n, "href"))
				}
			}
			// A URL parked in an attribute for the reader's own player to pick
			// up, e.g. Inoreader's enclosures_audio_player div. Nothing says it
			// is audio but the class and the file name, so demand one of them:
			// the same attribute carries video elsewhere.
			if carrier == "" {
				if u := nodeAttr(n, "data-media-url"); u != "" {
					if audioFile(u) || strings.Contains(nodeAttr(n, "class"), "audio") {
						carrier = mediaURL(u)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	for _, u := range []string{player, carrier, link} {
		if u != "" {
			return u
		}
	}
	return ""
}

// mediaSrc resolves an <audio>/<video> element's file: its own src, else the
// first <source> child that carries one.
func mediaSrc(n *html.Node) string {
	if u := mediaURL(nodeAttr(n, "src")); u != "" {
		return u
	}
	var out string
	for c := n.FirstChild; c != nil && out == ""; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "source" {
			out = mediaURL(nodeAttr(c, "src"))
		}
	}
	return out
}

// audioLink reports whether an <a> points at an audio file: either the app
// typed the enclosure for us, or the URL's own extension says so.
func audioLink(n *html.Node) bool {
	return strings.HasPrefix(nodeAttr(n, "data-type"), "audio/") || audioFile(nodeAttr(n, "href"))
}

// audioFile reports whether a URL names a file an <audio> element can play,
// ignoring the query string podcast trackers append.
func audioFile(raw string) bool {
	path := raw
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	path = strings.ToLower(path)
	for _, ext := range []string{".mp3", ".m4a", ".m4b", ".aac", ".ogg", ".oga", ".opus", ".wav", ".flac"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
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
func ImageURL(raw string) string { return mediaURL(raw) }

// mediaURL is that normalization for any kind of media reference.
func mediaURL(raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "//"): // protocol-relative
		return "https:" + s
	case strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "http://"):
		return s
	}
	return ""
}
