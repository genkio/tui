package folo

import (
	"html"
	"regexp"
	"strings"
)

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(?:script|style)>`)
	reBr          = regexp.MustCompile(`(?i)<br\s*/?>`)
	reListItem    = regexp.MustCompile(`(?i)<li[^>]*>`)
	reBlockClose  = regexp.MustCompile(`(?i)</(p|div|ul|ol|h[1-6]|tr|table|blockquote|section|article)>`)
	reTag         = regexp.MustCompile(`(?s)<[^>]+>`)
	reInlineSpace = regexp.MustCompile(`[ \t\f\v]+`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
)

// HTMLToText flattens entry HTML into readable plain text: block elements
// become line breaks, list items get a bullet, tags are stripped, and entities
// are decoded. Good enough for reading feed bodies, not a full renderer.
func HTMLToText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	s = reScriptStyle.ReplaceAllString(s, "")
	s = reBr.ReplaceAllString(s, "\n")
	s = reListItem.ReplaceAllString(s, "\n- ")
	s = reBlockClose.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimSpace(reInlineSpace.ReplaceAllString(ln, " "))
	}
	s = strings.Join(lines, "\n")
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
