package inoreader

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	gohtml "golang.org/x/net/html"
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

// HTMLToText flattens article HTML into readable plain text: block elements
// become line breaks, list items get a bullet, tags are stripped, and entities
// are decoded. Good enough for reading feed bodies, not a full renderer.
//
// Code and tables are the exception, and come out as Markdown: the flattening
// is what a paragraph survives, and a table put through it is a column of
// orphaned cells while a program is a wall of soft-wrapped lines. The card
// renders Markdown, so saying it in Markdown is what keeps them readable.
func HTMLToText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	s, blocks := liftBlocks(s)
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
	s = restoreBlocks(s, blocks)
	return strings.TrimSpace(s)
}

// The mark a lifted block leaves behind. A control character because the
// flattening below trims, squishes and collapses everything it can see, and the
// one thing it must not touch is a block whose whitespace is the content.
func blockMark(i int) string { return fmt.Sprintf("\x02tui-block-%d\x02", i) }

// liftBlocks pulls every <pre> and every table out of the HTML, rewrites them
// as Markdown, and leaves a mark where each one stood. Unparseable HTML is
// handed back as it came: the flattening below never fails, and half a lift is
// worse than none.
func liftBlocks(s string) (string, []string) {
	doc, err := gohtml.Parse(strings.NewReader(s))
	if err != nil {
		return s, nil
	}
	body := findNode(doc, "body")
	if body == nil {
		return s, nil
	}
	var blocks []string
	var walk func(*gohtml.Node)
	walk = func(n *gohtml.Node) {
		for ch := n.FirstChild; ch != nil; {
			next := ch.NextSibling
			md := ""
			if ch.Type == gohtml.ElementNode {
				switch ch.Data {
				case "pre":
					md = fencedCode(ch)
				case "table":
					md = pipeTable(ch)
				}
			}
			if md != "" {
				n.InsertBefore(&gohtml.Node{Type: gohtml.TextNode, Data: blockMark(len(blocks))}, ch)
				n.RemoveChild(ch)
				blocks = append(blocks, md)
			} else {
				walk(ch)
			}
			ch = next
		}
	}
	walk(body)
	if len(blocks) == 0 {
		return s, nil
	}
	var b strings.Builder
	for ch := body.FirstChild; ch != nil; ch = ch.NextSibling {
		if err := gohtml.Render(&b, ch); err != nil {
			return s, nil
		}
	}
	return b.String(), blocks
}

// restoreBlocks puts the lifted Markdown back, each on lines of its own: a
// fence or a table row that shares a line with a paragraph is not a fence or a
// table row.
func restoreBlocks(s string, blocks []string) string {
	for i, md := range blocks {
		s = strings.ReplaceAll(s, blockMark(i), "\n\n"+md+"\n\n")
	}
	if len(blocks) > 0 {
		s = reBlankLines.ReplaceAllString(s, "\n\n")
	}
	return s
}

var reLang = regexp.MustCompile(`(?:language|lang|highlight)-([a-zA-Z0-9+#-]+)`)

// fencedCode turns a <pre> into a fenced block, keeping the language the source
// tagged it with when there is one. The fence is long enough to hold whatever
// backticks the code itself carries.
func fencedCode(pre *gohtml.Node) string {
	code := strings.Trim(rawTextOf(pre), "\n")
	if strings.TrimSpace(code) == "" {
		return ""
	}
	lang := ""
	if inner := findNode(pre, "code"); inner != nil {
		if m := reLang.FindStringSubmatch(attr(inner, "class")); m != nil {
			lang = m[1]
		}
	}
	fence := "```"
	for strings.Contains(code, fence) {
		fence += "`"
	}
	lines := strings.Split(code, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return fence + lang + "\n" + strings.Join(lines, "\n") + "\n" + fence
}

// pipeTable turns a table into a GFM one. A table with a single column is left
// alone: the web is full of tables that are page furniture rather than data,
// and a one-column grid reads better as the paragraphs it already is.
func pipeTable(table *gohtml.Node) string {
	var rows [][]string
	var walk func(*gohtml.Node)
	walk = func(n *gohtml.Node) {
		if n.Type == gohtml.ElementNode && n.Data == "tr" {
			var row []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == gohtml.ElementNode && (c.Data == "td" || c.Data == "th") {
					row = append(row, cellText(c))
				}
			}
			if len(row) > 0 {
				rows = append(rows, row)
			}
			return // no nested table's rows: those come round on their own walk
		}
		if n.Type == gohtml.ElementNode && n.Data == "table" && n != table {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(table)
	if len(rows) == 0 {
		return ""
	}
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols < 2 {
		return ""
	}
	var b strings.Builder
	for i, r := range rows {
		for len(r) < cols {
			r = append(r, "")
		}
		fmt.Fprintf(&b, "| %s |\n", strings.Join(r[:cols], " | "))
		// GFM has no table without a header row, so the first row becomes one
		// whether the source called it that or not.
		if i == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", cols) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// cellText is one cell on one line, with the pipes escaped so they stay cell
// contents rather than becoming the grid.
func cellText(n *gohtml.Node) string {
	var b strings.Builder
	var f func(*gohtml.Node)
	f = func(n *gohtml.Node) {
		if n.Type == gohtml.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == gohtml.ElementNode && (n.Data == "br" || n.Data == "p" || n.Data == "div") {
			b.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return strings.ReplaceAll(squish(b.String()), "|", `\|`)
}

// rawTextOf is textOf without the squishing: inside a <pre>, the whitespace is
// the content.
func rawTextOf(n *gohtml.Node) string {
	var b strings.Builder
	var f func(*gohtml.Node)
	f = func(n *gohtml.Node) {
		if n.Type == gohtml.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return b.String()
}

func findNode(n *gohtml.Node, name string) *gohtml.Node {
	if n.Type == gohtml.ElementNode && n.Data == name {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findNode(c, name); got != nil {
			return got
		}
	}
	return nil
}
