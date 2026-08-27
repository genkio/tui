package chartcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrateAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "charts.json")
	at := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(map[string]any{
		"charts": map[string]Entry{"weekly": {FetchedAt: at, Body: json.RawMessage(`{"old":true}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, data, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "feed.db")
	cache := Load(path, "douban", legacy)
	if got := string(cache.Charts["weekly"].Body); got != `{"old":true}` {
		t.Fatalf("migrated body = %q", got)
	}
	cache.Charts["weekly"] = Entry{FetchedAt: at.Add(time.Hour), Body: json.RawMessage(`{"new":true}`)}
	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}
	cache.Close()

	reopened := Load(path, "douban", legacy)
	defer reopened.Close()
	if got := string(reopened.Charts["weekly"].Body); got != `{"new":true}` {
		t.Fatalf("reopened body = %q", got)
	}
}
