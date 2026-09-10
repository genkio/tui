package inoreader

import "testing"

func TestHTMLToText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"<p>Hello</p><p>World</p>", "Hello\nWorld"},
		{"line1<br>line2", "line1\nline2"},
		{"<ul><li>one</li><li>two</li></ul>", "- one\n- two"},
		{"a &amp; b &lt;c&gt;", "a & b <c>"},
		{"<script>evil()</script>visible", "visible"},
		{"<style>.x{}</style>text", "text"},
		{"<p>a</p>\n\n\n\n<p>b</p>", "a\n\nb"},
		{"  <div>  spaced   out  </div> ", "spaced out"},
	}
	for _, c := range cases {
		if got := HTMLToText(c.in); got != c.want {
			t.Errorf("HTMLToText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A program flattened into paragraphs is a wall of soft-wrapped lines, so a
// <pre> comes out as a fence — indentation, blank lines, language and all.
func TestHTMLToTextKeepsCodeAsAFence(t *testing.T) {
	in := `<p>like so:</p><pre><code class="language-lua">local apps = {
  { key = "c" },
}

return apps
</code></pre><p>after</p>`
	want := "like so:\n\n```lua\nlocal apps = {\n  { key = \"c\" },\n}\n\nreturn apps\n```\n\nafter"
	if got := HTMLToText(in); got != want {
		t.Errorf("HTMLToText() =\n%q\nwant\n%q", got, want)
	}
}

func TestHTMLToTextKeepsTablesAsTables(t *testing.T) {
	in := `<table><thead><tr><th>键</th><th>应用</th></tr></thead>` +
		`<tbody><tr><td>⌥C</td><td>Claude</td></tr><tr><td>⌥G</td><td>a | b</td></tr></tbody></table>`
	want := "| 键 | 应用 |\n| --- | --- |\n| ⌥C | Claude |\n| ⌥G | a \\| b |"
	if got := HTMLToText(in); got != want {
		t.Errorf("HTMLToText() =\n%q\nwant\n%q", got, want)
	}
}

// Most tables on the web are page furniture rather than data, and a single
// column of them reads better as the text it already is.
func TestHTMLToTextLeavesOneColumnTablesAlone(t *testing.T) {
	got := HTMLToText(`<table><tr><td>one</td></tr><tr><td>two</td></tr></table>`)
	if got != "one\ntwo" {
		t.Errorf("HTMLToText() = %q, want the rows as plain lines", got)
	}
}
