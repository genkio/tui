package core

import (
	"os"
	"path/filepath"
	"strings"
)

// StateDir is the optional single root for everything worth syncing between
// devices — credentials (env), per-app read state, per-app configs — so
// pointing it at a synced folder (Dropbox, iCloud, …) carries sessions and
// read marks across machines. Read from $TUI_STATE_DIR (the launcher's
// --state-dir exports it), with a leading ~ expanded and the result made
// absolute so subprocesses with a different cwd agree. Empty when unset:
// every file stays in its XDG default location.
//
// The browser login profile (~/.config/tui/profile) deliberately stays out:
// syncing a live Chromium profile corrupts it, and the captured session
// values in env are what the apps actually use.
func StateDir() string {
	dir := strings.TrimSpace(os.Getenv("TUI_STATE_DIR"))
	if dir == "" {
		return ""
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir[1:], "/"))
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return dir
}

// StatePath is where app persists file (e.g. read state):
// $TUI_STATE_DIR/state/<app>/<file> when the state dir is set, else
// $XDG_STATE_HOME/<app>/<file>, else ~/.local/state/<app>/<file>.
// Empty when no location can be resolved.
func StatePath(app, file string) string {
	if dir := StateDir(); dir != "" {
		return filepath.Join(dir, "state", app, file)
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, app, file)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", app, file)
}

// ConfigPath is where app keeps file (e.g. config.toml):
// $TUI_STATE_DIR/config/<app>/<file> when the state dir is set, else
// $XDG_CONFIG_HOME/<app>/<file>, else ~/.config/<app>/<file>.
// Empty when no location can be resolved.
func ConfigPath(app, file string) string {
	if dir := StateDir(); dir != "" {
		return filepath.Join(dir, "config", app, file)
	}
	dir := userConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, app, file)
}
