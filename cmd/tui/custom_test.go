package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func pluginsDir(t *testing.T) string {
	t.Helper()
	sync := t.TempDir()
	t.Setenv("TUI_SYNC_DIR", sync)
	dir := filepath.Join(sync, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
		customs.scan() // forget this test's plugins
	})
	return dir
}

func writePlugin(t *testing.T, dir, file, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Whatever parses in the sync dir's plugins/ is a source with no login to
// wait on, and one that doesn't parse is still listed, so its chip can go red.
func TestCustomPluginsAreSources(t *testing.T) {
	dir := pluginsDir(t)
	writePlugin(t, dir, "forum.js", `module.exports = { label: "frm", color: "#2a9d8f", fetch() { return [] } }`)
	writePlugin(t, dir, "broken.js", `module.exports = { fetch() { return [ } }`)
	writePlugin(t, dir, "notes.txt", "not a parser")
	writePlugin(t, dir, "reddit.js", `module.exports = { fetch() { return [] } }`)
	writePlugin(t, dir, "Bad Name.js", `module.exports = { fetch() { return [] } }`)

	apps := authedFeedApps(t.TempDir())
	for _, want := range []string{"broken", "forum"} {
		if !slices.Contains(apps, want) {
			t.Errorf("%s missing from %v", want, apps)
		}
	}
	for _, not := range []string{"notes", "Bad Name", "bad name"} {
		if slices.Contains(apps, not) {
			t.Errorf("%s should not be a source: %v", not, apps)
		}
	}
	// A file named after a built-in would file its posts under reddit's key.
	if n := countOf(apps, "reddit"); n > 1 {
		t.Errorf("reddit listed %d times", n)
	}
	if got := appLabel("forum"); got != "frm" {
		t.Errorf("label = %q, want the parser's", got)
	}
	if got := appColor("forum"); got != "#2a9d8f" {
		t.Errorf("color = %q, want the parser's", got)
	}
	if got := appLabel("broken"); got != "broken" {
		t.Errorf("label = %q, want the name when the parser says none", got)
	}
	if got := appColor("broken"); got != customColor {
		t.Errorf("color = %q, want the custom default", got)
	}
}

func countOf(xs []string, x string) int {
	n := 0
	for _, y := range xs {
		if y == x {
			n++
		}
	}
	return n
}

// Editing a parser takes effect on the next page load, without a restart.
func TestCustomPluginEditIsPickedUp(t *testing.T) {
	dir := pluginsDir(t)
	writePlugin(t, dir, "forum.js", `module.exports = { label: "old", fetch() { return [] } }`)
	customs.scan()
	if got := appLabel("forum"); got != "old" {
		t.Fatalf("label = %q", got)
	}
	writePlugin(t, dir, "forum.js", `module.exports = { label: "newer", fetch() { return [] } }`)
	customs.scan()
	if got := appLabel("forum"); got != "newer" {
		t.Fatalf("label = %q after the edit, want newer", got)
	}
	os.Remove(filepath.Join(dir, "forum.js"))
	if apps := customs.scan(); slices.Contains(apps, "forum") {
		t.Fatalf("a removed parser is still a source: %v", apps)
	}
}

// The sweep asks for its depth and a thread hands over a thousand posts, so
// the newest are kept, all filed under the plugin's name.
func TestFetchCustomKeepsTheNewest(t *testing.T) {
	dir := pluginsDir(t)
	writePlugin(t, dir, "forum.js", `module.exports = { fetch() {
  return [1, 2, 3, 4, 5].map(n => ({ id: "p" + n, ts: new Date(Date.UTC(2025, 0, n)) }));
} }`)
	customs.scan()
	p, ok := customs.lookup("forum")
	if !ok {
		t.Fatal("forum not registered")
	}
	items, stale, err := subprocessFetch(t.TempDir())(context.Background(), "forum", 2, time.Now())
	if err != nil || stale {
		t.Fatalf("err = %v, stale = %v", err, stale)
	}
	if len(items) != 2 || items[0].ID != "p5" || items[1].ID != "p4" {
		t.Fatalf("got %+v, want p5 and p4", items)
	}
	for _, it := range items {
		if it.App != p.name {
			t.Errorf("%s filed under %q", it.ID, it.App)
		}
	}
}

// A custom source's read state is the cache's alone: there is no subcommand
// to tell, and trying would retry forever.
func TestCustomMarksNeedNoSubprocess(t *testing.T) {
	mark := subprocessMark("/nonexistent")
	for _, app := range []string{"forum", "a-plugin-since-deleted"} {
		if err := mark(context.Background(), app, []string{"1"}); err != nil {
			t.Errorf("%s: %v", app, err)
		}
	}
}
