package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plugin runs before the launcher parses anything, so --sync-dir has to be
// taken out of the arguments here or the plugin's own flag set refuses it.
func TestTakeSyncDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		want []string
		dir  string
	}{
		{
			"spaced, one dash",
			[]string{"tui", "x", "-sync-dir", "/box/tui", "--json"},
			[]string{"tui", "x", "--json"},
			"/box/tui",
		},
		{
			"spaced, two dashes",
			[]string{"tui", "x", "--sync-dir", "/box/tui"},
			[]string{"tui", "x"},
			"/box/tui",
		},
		{
			"joined by =",
			[]string{"tui", "reddit", "--sync-dir=/box/tui", "--count"},
			[]string{"tui", "reddit", "--count"},
			"/box/tui",
		},
		{
			// The path is stored expanded: a plugin resolving it may be running
			// from anywhere, and ~ means nothing to filepath.Join.
			"home-relative",
			[]string{"tui", "x", "--sync-dir", "~/box/tui"},
			[]string{"tui", "x"},
			filepath.Join(home, "box", "tui"),
		},
		{
			"absent",
			[]string{"tui", "x", "--json"},
			[]string{"tui", "x", "--json"},
			"",
		},
		{
			// Not ours: an app flag that merely starts the same way stays put.
			"lookalike flag",
			[]string{"tui", "x", "--sync-dirs", "/box"},
			[]string{"tui", "x", "--sync-dirs", "/box"},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TUI_SYNC_DIR", "")
			got := takeSyncDir(c.args)
			if strings.Join(got, " ") != strings.Join(c.want, " ") {
				t.Errorf("args = %v, want %v", got, c.want)
			}
			if os.Getenv("TUI_SYNC_DIR") != c.dir {
				t.Errorf("TUI_SYNC_DIR = %q, want %q", os.Getenv("TUI_SYNC_DIR"), c.dir)
			}
		})
	}
}
