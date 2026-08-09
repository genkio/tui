// Package config loads douban-tui settings from defaults, an optional TOML
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

// DefaultCharts are the douban 榜单 mixed into the feed out of the box: the
// weekly best films, series and shows. Each id is a subject_collection, the
// tail of an m.douban.com/subject_collection/<id> address. Setting
// `charts = []` in the config file turns them off.
var DefaultCharts = []string{
	"movie_weekly_best",
	"tv_global_best_weekly",
	"show_global_best_weekly",
}

// Config is the fully resolved configuration the app runs with.
type Config struct {
	Theme       string   `toml:"theme"`        // auto | light | dark
	Refresh     string   `toml:"refresh"`      // auto-refresh interval, e.g. "2m"; empty = off
	MaxStatuses int      `toml:"max_statuses"` // statuses to fetch per refresh
	UnreadOnly  bool     `toml:"unread_only"`  // hide statuses already marked read (vs grey them in place)
	Charts      []string `toml:"charts"`       // subject_collection ids mixed into the feed; empty list = none
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Theme:       "auto",
		MaxStatuses: 50,
		UnreadOnly:  true,
		Charts:      append([]string{}, DefaultCharts...),
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

// DefaultPath is $TUI_STATE_DIR/config/douban-tui/config.toml when a shared
// state dir is set, else $XDG_CONFIG_HOME/douban-tui/config.toml, falling back
// to ~/.config/douban-tui/config.toml.
func DefaultPath() string {
	return core.ConfigPath("douban-tui", "config.toml")
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
	if v := os.Getenv("DBTUI_THEME"); v != "" {
		cfg.Theme = v
	}
	if v := os.Getenv("DBTUI_REFRESH"); v != "" {
		cfg.Refresh = v
	}
	if v := os.Getenv("DBTUI_MAX_STATUSES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxStatuses = n
		}
	}
	if v := os.Getenv("DBTUI_UNREAD_ONLY"); v != "" {
		cfg.UnreadOnly = truthy(v)
	}
	// set-but-empty is meaningful here: DBTUI_CHARTS= turns the charts off
	if v, ok := os.LookupEnv("DBTUI_CHARTS"); ok {
		cfg.Charts = splitList(v)
	}
}

// splitList reads a comma-separated env override into a list, dropping blanks
// so a stray comma cannot ask for a chart with no name.
func splitList(v string) []string {
	out := []string{}
	for _, f := range strings.Split(v, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// Cookie returns the captured douban.com browser-session cookie set from the
// environment.
func Cookie() string { return os.Getenv("DBTUI_COOKIE") }

// UserAgent returns an optional override for the request User-Agent, matching
// the browser the cookie came from.
func UserAgent() string { return os.Getenv("DBTUI_UA") }

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

const authHelp = `no douban session found in the environment.

Set DBTUI_COOKIE to a logged-in douban browser session (the whole cookie set,
including "dbcl2=..."). The easiest way is the bundled capture helper, which
opens a browser, lets you log in, and writes it for you:

  make auth

See the README "Authentication" section for details.`
