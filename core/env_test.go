package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env")
	content := "# a comment\n" +
		"export XTUI_AUTH_TOKEN='abc123'\n" +
		"\n" +
		"INOREADER_COOKIE=bare\n" +
		`Q="dquoted"` + "\n" +
		`ESC='a'\''b'` + "\n" // shell-escaped single quote -> a'b
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ParseEnvFile(path)
	want := map[string]string{
		"XTUI_AUTH_TOKEN":  "abc123",
		"INOREADER_COOKIE": "bare",
		"Q":                "dquoted",
		"ESC":              "a'b",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("ParseEnvFile[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseEnvFileMissing(t *testing.T) {
	if got := ParseEnvFile(filepath.Join(t.TempDir(), "nope")); len(got) != 0 {
		t.Errorf("missing file should yield empty map, got %v", got)
	}
}

// LoadUserEnv fills unset vars from the file but never clobbers a value already
// in the environment (a real shell export wins).
func TestLoadUserEnvSkipsSetKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "tui"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "TUI_TEST_FROMFILE=filevalue\nTUI_TEST_PRESET=fromfile\n"
	if err := os.WriteFile(filepath.Join(dir, "tui", "env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUI_TEST_PRESET", "fromshell") // already set: must win
	t.Setenv("TUI_TEST_FROMFILE", "")        // unset-equivalent; cleaned up by t.Setenv

	LoadUserEnv()

	if got := os.Getenv("TUI_TEST_FROMFILE"); got != "filevalue" {
		t.Errorf("unset var not loaded from file: %q", got)
	}
	if got := os.Getenv("TUI_TEST_PRESET"); got != "fromshell" {
		t.Errorf("preset var was clobbered: %q, want fromshell", got)
	}
}
