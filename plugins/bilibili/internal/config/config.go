// Package config loads bilibili-tui settings from defaults, an optional TOML
// file, and environment overrides. It never reads or stores the session secrets;
// those live only in the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/genkio/tui/core"
)

// Config is the fully resolved configuration the app runs with.
type Config struct {
	Theme      string `toml:"theme"`       // auto | light | dark
	Refresh    string `toml:"refresh"`     // auto-refresh interval, e.g. "2m"; empty = off
	MaxVideos  int    `toml:"max_videos"`  // video posts to fetch per refresh
	UnreadOnly bool   `toml:"unread_only"` // hide posts already marked read (vs grey them in place)
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Theme:      "auto",
		MaxVideos:  40,
		UnreadOnly: true,
	}
}

// Load resolves configuration from defaults, then the TOML file (the given path,
// or the default location if empty), then environment overrides.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = DefaultPath()
	}
	if path != "" {
		if err := mergeFile(&cfg, path); err != nil {
			return cfg, err
		}
	}
	applyEnv(&cfg)
	return cfg, nil
}

// DefaultPath is $TUI_SYNC_DIR/config/bilibili-tui/config.toml when a shared
// sync dir is set, else $XDG_CONFIG_HOME/bilibili-tui/config.toml, falling back
// to ~/.config/bilibili-tui/config.toml.
func DefaultPath() string {
	return core.ConfigPath("bilibili-tui", "config.toml")
}

func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // a missing config file is fine; defaults stand
	}
	if err != nil {
		return fmt.Errorf("reading config %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parsing config %s: %w", path, err)
	}
	return nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("BLTUI_THEME"); v != "" {
		cfg.Theme = v
	}
	if v := os.Getenv("BLTUI_REFRESH"); v != "" {
		cfg.Refresh = v
	}
	if v := os.Getenv("BLTUI_MAX_VIDEOS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxVideos = n
		}
	}
	if v := os.Getenv("BLTUI_UNREAD_ONLY"); v != "" {
		cfg.UnreadOnly = truthy(v)
	}
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// Cookie returns the captured bilibili.com browser-session cookie set from the
// environment.
func Cookie() string { return os.Getenv("BLTUI_COOKIE") }

// UserAgent returns an optional override for the request User-Agent, matching the
// browser the cookie came from.
func UserAgent() string { return os.Getenv("BLTUI_UA") }

// ValidateAuth checks that the session cookie is present, without reading more
// than its presence.
func ValidateAuth() error {
	if strings.TrimSpace(Cookie()) == "" {
		return errors.New(authHelp)
	}
	return nil
}

// RefreshInterval parses the configured auto-refresh interval (e.g. "2m").
// Empty, malformed, or non-positive values mean "off" (0).
func (c Config) RefreshInterval() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(c.Refresh))
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

const authHelp = `no bilibili session found in the environment.

Set BLTUI_COOKIE to a logged-in bilibili browser session (the whole cookie set,
including "SESSDATA=..."). The easiest way is the bundled capture helper, which
opens a browser, lets you log in, and writes it for you:

  make auth

See the README "Authentication" section for details.`
