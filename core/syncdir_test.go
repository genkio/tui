package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncDirUnset(t *testing.T) {
	t.Setenv("TUI_SYNC_DIR", "")
	if got := SyncDir(); got != "" {
		t.Fatalf("unset TUI_SYNC_DIR should yield empty, got %q", got)
	}
}

func TestSyncDirExpandsTilde(t *testing.T) {
	t.Setenv("TUI_SYNC_DIR", "~/Dropbox/tui")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	want := filepath.Join(home, "Dropbox", "tui")
	if got := SyncDir(); got != want {
		t.Fatalf("SyncDir() = %q, want %q", got, want)
	}
}

func TestPathsFollowSyncDir(t *testing.T) {
	t.Setenv("TUI_SYNC_DIR", "/sync/tui")
	if got, want := UserEnvPath(), filepath.Join("/sync/tui", "env"); got != want {
		t.Fatalf("UserEnvPath() = %q, want %q", got, want)
	}
	if got, want := StatePath("x-tui", "read.json"), filepath.Join("/sync/tui", "state", "x-tui", "read.json"); got != want {
		t.Fatalf("StatePath() = %q, want %q", got, want)
	}
	if got, want := ConfigPath("x-tui", "config.toml"), filepath.Join("/sync/tui", "config", "x-tui", "config.toml"); got != want {
		t.Fatalf("ConfigPath() = %q, want %q", got, want)
	}
}

func TestPathsFallBackToXDG(t *testing.T) {
	t.Setenv("TUI_SYNC_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	if got, want := StatePath("x-tui", "read.json"), filepath.Join("/xdg/state", "x-tui", "read.json"); got != want {
		t.Fatalf("StatePath() = %q, want %q", got, want)
	}
	if got, want := ConfigPath("x-tui", "config.toml"), filepath.Join("/xdg/config", "x-tui", "config.toml"); got != want {
		t.Fatalf("ConfigPath() = %q, want %q", got, want)
	}
	if got, want := UserEnvPath(), filepath.Join("/xdg/config", "tui", "env"); got != want {
		t.Fatalf("UserEnvPath() = %q, want %q", got, want)
	}
}
