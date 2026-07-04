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
