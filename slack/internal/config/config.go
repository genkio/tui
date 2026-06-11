// Package config loads TUI settings from sensible defaults, an optional TOML
// file, and environment overrides. It never reads or stores token values; auth
// secrets live only in the environment and are forwarded to the spawned server.
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
	Theme       string        `toml:"theme"` // auto | light | dark
	SlackDomain string        `toml:"slack_domain"`
	Refresh     string        `toml:"refresh"` // auto-refresh interval, e.g. "30s"; empty = off
	Server      ServerConfig  `toml:"server"`
	Unreads     UnreadsConfig `toml:"unreads"`
}

// ServerConfig describes how to launch slack-mcp-server over stdio.
type ServerConfig struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}

// UnreadsConfig holds defaults passed to the conversations_unreads tool.
type UnreadsConfig struct {
	ChannelTypes          string `toml:"channel_types"` // all|dm|group_dm|partner|internal
	MaxChannels           int    `toml:"max_channels"`
	MaxMessagesPerChannel int    `toml:"max_messages_per_channel"`
	MentionsOnly          bool   `toml:"mentions_only"`
}

// Default returns the built-in configuration. The server is launched via npx
// against a pinned version so a fresh user needs nothing installed but Node.
func Default() Config {
	return Config{
		Theme: "auto",
		Server: ServerConfig{
			Command: "npx",
			Args:    []string{"-y", "slack-mcp-server@1.3.0", "--transport", "stdio"},
		},
		Unreads: UnreadsConfig{
			ChannelTypes:          "all",
			MaxChannels:           50,
			MaxMessagesPerChannel: 10,
			MentionsOnly:          false,
		},
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

// DefaultPath is $XDG_CONFIG_HOME/slack-tui/config.toml, falling back to
// ~/.config/slack-tui/config.toml.
func DefaultPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "slack-tui", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "slack-tui", "config.toml")
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
	if v := os.Getenv("SLACK_TUI_THEME"); v != "" {
		cfg.Theme = v
	}
	if v := os.Getenv("SLACK_TUI_SLACK_DOMAIN"); v != "" {
		cfg.SlackDomain = v
	}
	if v := os.Getenv("SLACK_TUI_REFRESH"); v != "" {
		cfg.Refresh = v
	}
	if v := os.Getenv("SLACK_TUI_SERVER_COMMAND"); v != "" {
		cfg.Server.Command = v
	}
	if v := os.Getenv("SLACK_TUI_SERVER_ARGS"); v != "" {
		cfg.Server.Args = strings.Fields(v)
	}
	if v := os.Getenv("SLACK_TUI_CHANNEL_TYPES"); v != "" {
		cfg.Unreads.ChannelTypes = v
	}
	if v := os.Getenv("SLACK_TUI_MAX_CHANNELS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Unreads.MaxChannels = n
		}
	}
	if v := os.Getenv("SLACK_TUI_MAX_MESSAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Unreads.MaxMessagesPerChannel = n
		}
	}
	if v := os.Getenv("SLACK_TUI_MENTIONS_ONLY"); v != "" {
		cfg.Unreads.MentionsOnly = truthy(v)
	}
}

// ValidateAuth checks that a usable token combination is present in the
// environment without ever reading the values. The server needs an xoxp or
// xoxb token, or the xoxc+xoxd browser-session pair.
func ValidateAuth() error {
	has := func(name string) bool { return os.Getenv(name) != "" }
	switch {
	case has("SLACK_MCP_XOXP_TOKEN"), has("SLACK_MCP_XOXB_TOKEN"):
		return nil
	case has("SLACK_MCP_XOXC_TOKEN") && has("SLACK_MCP_XOXD_TOKEN"):
		return nil
	default:
		return errors.New(authHelp)
	}
}

// MarkToolEnabled reports whether mark-as-read is turned on for the server.
// The server advertises conversations_mark unconditionally but refuses to run
// it unless SLACK_MCP_MARK_TOOL is truthy; we forward that same env var, so
// reading it here matches what the server will do.
func MarkToolEnabled() bool {
	return truthy(os.Getenv("SLACK_MCP_MARK_TOOL"))
}

// ReactionToolEnabled reports whether reactions are turned on for the server.
// Unlike the mark tool, SLACK_MCP_REACTION_TOOL also accepts channel lists
// ("C123,D456") and exclusions ("!C123"), so any non-empty value enables the
// feature; the server enforces the per-channel scoping itself.
func ReactionToolEnabled() bool {
	return strings.TrimSpace(os.Getenv("SLACK_MCP_REACTION_TOOL")) != ""
}

// SlackBaseURL normalizes the configured workspace into a base URL such as
// "https://acme.slack.com", or "" when unset. It accepts a bare subdomain
// ("acme"), a host ("acme.slack.com"), or a full URL.
// RefreshInterval parses the configured auto-refresh interval (e.g. "30s").
// Empty, malformed, or non-positive values mean "off" (0).
func (c Config) RefreshInterval() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(c.Refresh))
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

func (c Config) SlackBaseURL() string {
	d := strings.TrimSuffix(strings.TrimSpace(c.SlackDomain), "/")
	switch {
	case d == "":
		return ""
	case strings.Contains(d, "://"):
		return d
	case strings.Contains(d, "."):
		return "https://" + d
	default:
		return "https://" + d + ".slack.com"
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

const authHelp = `no Slack credentials found in the environment.

Set one of the following before running slack-tui:

  SLACK_MCP_XOXP_TOKEN   OAuth user token (recommended)
  SLACK_MCP_XOXB_TOKEN   bot token
  SLACK_MCP_XOXC_TOKEN + SLACK_MCP_XOXD_TOKEN   browser-session tokens

See the README for how to obtain a token.`
