package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadResolvesFileThenEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("max_videos = 12\nrefresh = \"3m\"\nunread_only = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxVideos != 12 || cfg.UnreadOnly || cfg.RefreshInterval() != 3*time.Minute {
		t.Fatalf("the file should win over the defaults: %+v", cfg)
	}

	// the environment wins over the file
	t.Setenv("BLTUI_MAX_VIDEOS", "30")
	t.Setenv("BLTUI_REFRESH", "90s")
	t.Setenv("BLTUI_UNREAD_ONLY", "yes")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxVideos != 30 || !cfg.UnreadOnly || cfg.RefreshInterval() != 90*time.Second {
		t.Fatalf("the env should win over the file: %+v", cfg)
	}

	// a malformed interval is off, not an error: the app still runs
	t.Setenv("BLTUI_REFRESH", "soon")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RefreshInterval() != 0 {
		t.Errorf("refresh = %v, want off", cfg.RefreshInterval())
	}
}

func TestLoadWithoutAFileKeepsTheDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Default() {
		t.Fatalf("a missing config file should leave the defaults: %+v", cfg)
	}
}

func TestValidateAuthAsksForTheCookie(t *testing.T) {
	t.Setenv("BLTUI_COOKIE", "")
	err := ValidateAuth()
	if err == nil {
		t.Fatal("a missing session should be an error")
	}
	if !strings.Contains(err.Error(), "SESSDATA") {
		t.Errorf("the help should name the cookie that matters: %q", err)
	}

	t.Setenv("BLTUI_COOKIE", "SESSDATA=abc; bili_jct=def")
	if err := ValidateAuth(); err != nil {
		t.Errorf("a present session should validate: %v", err)
	}
}
