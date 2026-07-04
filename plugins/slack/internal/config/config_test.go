package config

import (
	"testing"
	"time"
)

func TestRefreshInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"":        0,
		"30s":     30 * time.Second,
		"2m":      2 * time.Minute,
		"garbage": 0,
		"-5s":     0,
	}
	for in, want := range cases {
		if got := (Config{Refresh: in}).RefreshInterval(); got != want {
			t.Errorf("RefreshInterval(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestReactionToolEnabled(t *testing.T) {
	cases := map[string]bool{
		"":          false,
		"   ":       false,
		"true":      true,
		"C123,D456": true, // channel allowlist
		"!C123":     true, // exclusion
	}
	for in, want := range cases {
		t.Setenv("SLACK_MCP_REACTION_TOOL", in)
		if got := ReactionToolEnabled(); got != want {
			t.Errorf("ReactionToolEnabled(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSlackBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"acme":                    "https://acme.slack.com",
		"acme.slack.com":          "https://acme.slack.com",
		"https://acme.slack.com":  "https://acme.slack.com",
		"https://acme.slack.com/": "https://acme.slack.com",
		"  acme  ":                "https://acme.slack.com",
	}
	for in, want := range cases {
		if got := (Config{SlackDomain: in}).SlackBaseURL(); got != want {
			t.Errorf("SlackBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
