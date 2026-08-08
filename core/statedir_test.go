package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateDirUnset(t *testing.T) {
	t.Setenv("TUI_STATE_DIR", "")
	if got := StateDir(); got != "" {
		t.Fatalf("unset TUI_STATE_DIR should yield empty, got %q", got)
	}
}

func TestStateDirExpandsTilde(t *testing.T) {
	t.Setenv("TUI_STATE_DIR", "~/Dropbox/tui")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	want := filepath.Join(home, "Dropbox", "tui")
	if got := StateDir(); got != want {
		t.Fatalf("StateDir() = %q, want %q", got, want)
	}
}

func TestPathsFollowStateDir(t *testing.T) {
	t.Setenv("TUI_STATE_DIR", "/sync/tui")
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
	t.Setenv("TUI_STATE_DIR", "")
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
