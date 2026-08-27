// Package config loads reddit-tui settings from defaults, an optional TOML
// file, and environment overrides. It never reads or stores the session
// secrets; those live only in the environment.
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
	MaxPosts   int    `toml:"max_posts"`   // posts to fetch per refresh
	UnreadOnly bool   `toml:"unread_only"` // hide posts already marked read (vs grey them in place)
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Theme:      "auto",
		MaxPosts:   50,
		UnreadOnly: true,
	}
}

// Load resolves configuration from defaults, then the TOML file (the given
// path, or the default location if empty), then environment overrides.
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

// DefaultPath is $TUI_SYNC_DIR/config/reddit-tui/config.toml when a shared
// sync dir is set, else $XDG_CONFIG_HOME/reddit-tui/config.toml, falling back
// to ~/.config/reddit-tui/config.toml.
func DefaultPath() string {
	return core.ConfigPath("reddit-tui", "config.toml")
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
	if v := os.Getenv("RDTUI_THEME"); v != "" {
		cfg.Theme = v
	}
	if v := os.Getenv("RDTUI_REFRESH"); v != "" {
		cfg.Refresh = v
	}
	if v := os.Getenv("RDTUI_MAX_POSTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxPosts = n
		}
	}
	if v := os.Getenv("RDTUI_UNREAD_ONLY"); v != "" {
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

// Cookie returns the captured reddit.com browser-session cookie set from the
// environment.
func Cookie() string { return os.Getenv("RDTUI_COOKIE") }

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

const authHelp = `no reddit session found in the environment.

Set RDTUI_COOKIE to a logged-in reddit browser session (the whole cookie set,
e.g. "reddit_session=...; token_v2=..."). The easiest way is the bundled capture
helper, which opens a browser, lets you log in, and writes it for you:

  make auth

See the README "Authentication" section for details.`
