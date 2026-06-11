// Package config loads x-tui settings from defaults, an optional TOML file, and
// environment overrides. It never reads or stores the session secrets
// (auth_token / ct0); those live only in the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the fully resolved configuration the app runs with.
type Config struct {
	Theme      string `toml:"theme"`       // auto | light | dark
	Refresh    string `toml:"refresh"`     // auto-refresh interval, e.g. "2m"; empty = off
	MaxTweets  int    `toml:"max_tweets"`  // posts to fetch per tab
	DefaultTab string `toml:"default_tab"` // foryou | following
	Lang       string `toml:"lang"`        // x-twitter-client-language
	Bearer     string `toml:"-"`           // optional override; secret-ish, env only
}

// Default returns the built-in configuration: the chronological Following feed,
// themed to match the terminal.
func Default() Config {
	return Config{
		Theme:      "auto",
		MaxTweets:  50,
		DefaultTab: "following",
		Lang:       "en",
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

// DefaultPath is $XDG_CONFIG_HOME/x-tui/config.toml, falling back to
// ~/.config/x-tui/config.toml.
func DefaultPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "x-tui", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "x-tui", "config.toml")
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
	if v := os.Getenv("XTUI_THEME"); v != "" {
		cfg.Theme = v
	}
	if v := os.Getenv("XTUI_REFRESH"); v != "" {
		cfg.Refresh = v
	}
	if v := os.Getenv("XTUI_MAX_TWEETS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxTweets = n
		}
	}
	if v := os.Getenv("XTUI_DEFAULT_TAB"); v != "" {
		cfg.DefaultTab = v
	}
	if v := os.Getenv("XTUI_LANG"); v != "" {
		cfg.Lang = v
	}
	if v := os.Getenv("XTUI_BEARER"); v != "" {
		cfg.Bearer = v
	}
}

// AuthToken returns the browser session auth_token cookie from the environment.
func AuthToken() string { return os.Getenv("XTUI_AUTH_TOKEN") }

// CSRF returns the ct0 token from the environment (sent as both the ct0 cookie
// and the x-csrf-token header).
func CSRF() string { return os.Getenv("XTUI_CT0") }

// ValidateAuth checks that both session secrets are present, without reading
// more than their presence.
func ValidateAuth() error {
	if strings.TrimSpace(AuthToken()) == "" || strings.TrimSpace(CSRF()) == "" {
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

const authHelp = `no x.com session found in the environment.

Set XTUI_AUTH_TOKEN and XTUI_CT0 to a logged-in x.com browser session. The
easiest way is the bundled capture helper, which opens a browser, lets you log
in, and writes both to .env for you:

  make auth

See the README "Authentication" section for details.`
