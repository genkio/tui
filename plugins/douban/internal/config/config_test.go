package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChartsResolution(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		path := filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "_")+".toml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// no config file: the built-in charts stand
	cfg, err := Load(write(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Charts) != len(DefaultCharts) || cfg.Charts[0] != DefaultCharts[0] {
		t.Errorf("charts = %v, want the defaults %v", cfg.Charts, DefaultCharts)
	}

	// an empty list is a choice, not an omission: it turns the charts off
	cfg, err = Load(write("charts = []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Charts) != 0 {
		t.Errorf("charts = %v, want none", cfg.Charts)
	}

	cfg, err = Load(write(`charts = ["book_weekly_best"]` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Charts) != 1 || cfg.Charts[0] != "book_weekly_best" {
		t.Errorf("charts = %v, want the configured one", cfg.Charts)
	}

	// the environment wins over the file, and set-but-empty means off
	t.Setenv("DBTUI_CHARTS", " movie_weekly_best , tv_global_best_weekly ,")
	cfg, err = Load(write("charts = []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Charts) != 2 || cfg.Charts[1] != "tv_global_best_weekly" {
		t.Errorf("charts = %v, want the two named in the env", cfg.Charts)
	}
	t.Setenv("DBTUI_CHARTS", "")
	cfg, err = Load(write(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Charts) != 0 {
		t.Errorf("charts = %v, want none", cfg.Charts)
	}
}
