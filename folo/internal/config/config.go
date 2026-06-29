// Package config loads folo-tui settings from defaults, an optional TOML file,
// and environment overrides. It never reads or stores the session cookie; that
// secret lives only in the environment.
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

// defaultUserAgent makes requests look like the web client they mimic; some
// endpoints are unhappy with a missing or non-browser User-Agent.
const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

// Config is the fully resolved configuration the app runs with.
type Config struct {
	Theme       string `toml:"theme"`        // auto | light | dark
	Refresh     string `toml:"refresh"`      // auto-refresh interval, e.g. "5m"; empty = off
	BaseURL     string `toml:"base_url"`     // Folo API host
	WebURL      string `toml:"web_url"`      // Folo web app origin (Origin/Referer header)
	MaxArticles int    `toml:"max_articles"` // how many articles to fetch per load
	UnreadOnly  bool   `toml:"unread_only"`  // pending (unread) triage vs all articles
	UserAgent   string `toml:"user_agent"`
}

// Default returns the built-in configuration: pending (unread) triage of the
// Articles timeline, themed to match the terminal.
func Default() Config {
	return Config{
		Theme:       "auto",
		BaseURL:     "https://api.folo.is",
		WebURL:      "https://app.folo.is",
		MaxArticles: 50,
		UnreadOnly:  true,
		UserAgent:   defaultUserAgent,
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

// DefaultPath is $XDG_CONFIG_HOME/folo-tui/config.toml, falling back to
// ~/.config/folo-tui/config.toml.
func DefaultPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "folo-tui", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "folo-tui", "config.toml")
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
	if v := os.Getenv("FOLO_TUI_THEME"); v != "" {
		cfg.Theme = v
	}
	if v := os.Getenv("FOLO_TUI_REFRESH"); v != "" {
		cfg.Refresh = v
	}
	if v := os.Getenv("FOLO_TUI_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("FOLO_TUI_WEB_URL"); v != "" {
		cfg.WebURL = v
	}
	if v := os.Getenv("FOLO_TUI_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxArticles = n
		}
	}
	if v := os.Getenv("FOLO_TUI_UNREAD_ONLY"); v != "" {
		cfg.UnreadOnly = truthy(v)
	}
	if v := os.Getenv("FOLO_TUI_USER_AGENT"); v != "" {
		cfg.UserAgent = v
	}
}

// Cookie returns the browser session cookie from the environment.
func Cookie() string { return os.Getenv("FOLO_COOKIE") }

// ValidateAuth checks that the session cookie is present without reading more
// than its presence.
func ValidateAuth() error {
	if strings.TrimSpace(Cookie()) != "" {
		return nil
	}
	return errors.New(authHelp)
}

// RefreshInterval parses the configured auto-refresh interval (e.g. "5m").
// Empty, malformed, or non-positive values mean "off" (0).
func (c Config) RefreshInterval() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(c.Refresh))
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

const authHelp = `no Folo session cookie found in the environment.

Set FOLO_COOKIE to a logged-in folo.is session cookie. The easiest way is the
bundled capture helper, which opens a browser, lets you log in, and writes the
cookie to .env for you:

  make auth

See the README "Authentication" section for details.`
