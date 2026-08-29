package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

func TestFeedDBMigratesJSONOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TUI_SYNC_DIR", dir)
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "local"))
	state := filepath.Join(dir, "state", "tui")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	feed := feedFile{
		Items:  []*feedEntry{{Wire: item("x", "1", "feed title").Wire(), FirstSeen: now.Format(time.RFC3339)}},
		Status: map[string]appStatus{"x": {At: now.Format(time.RFC3339)}},
		Swept:  now.Format(time.RFC3339),
	}
	saved := savedFile{Items: []savedItem{{Wire: item("reddit", "2", "saved title").Wire(), SavedAt: now.Format(time.RFC3339)}}}
	words := keywordFile{Keywords: []string{"spoiler"}}
	blocked := blockedFile{Items: []blockedItem{{Wire: item("folo", "3", "blocked title").Wire(), BlockedAt: now.Format(time.RFC3339), Keyword: "spoiler"}}}
	writeJSON := func(name string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON("feed.json", feed)
	writeJSON("saved.json", saved)
	writeJSON("keywords.json", words)
	writeJSON("blocked.json", blocked)
	readDir := filepath.Join(dir, "state", "x-tui")
	if err := os.MkdirAll(readDir, 0o755); err != nil {
		t.Fatal(err)
	}
	readData, err := json.Marshal(map[string]any{"read": map[string]int64{"tweet-1": 123}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readDir, "read.json"), readData, 0o644); err != nil {
		t.Fatal(err)
	}
	chartDir := filepath.Join(dir, "state", "douban-tui")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	chartData, err := json.Marshal(map[string]any{
		"charts": map[string]any{"weekly": map[string]any{"fetched_at": now, "body": map[string]any{"ok": true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "charts.json"), chartData, 0o644); err != nil {
		t.Fatal(err)
	}

	db, _, err := prepareFeedDB()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := loadFeedCacheDB(db)
	if err != nil {
		t.Fatal(err)
	}
	store, err := loadSavedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	block, err := loadBlockerDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if cache.unreadCount() != 1 || store.count() != 1 || block.keywordCount() != 1 || block.count() != 1 {
		t.Fatalf("migration lost state: feed=%d saved=%d keywords=%d blocked=%d", cache.unreadCount(), store.count(), block.keywordCount(), block.count())
	}
	var readCount, chartCount int
	if err := db.db.QueryRow(`SELECT count(*) FROM read_markers WHERE app='x'`).Scan(&readCount); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM chart_cache WHERE app='douban'`).Scan(&chartCount); err != nil {
		t.Fatal(err)
	}
	if readCount != 1 || chartCount != 1 {
		t.Fatalf("plugin migration lost state: read=%d charts=%d", readCount, chartCount)
	}
	if err := store.remove("reddit", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`DELETE FROM read_markers; DELETE FROM chart_cache`); err != nil {
		t.Fatal(err)
	}
	if err := db.close(); err != nil {
		t.Fatal(err)
	}

	db, _, err = prepareFeedDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	store, err = loadSavedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if store.count() != 0 {
		t.Fatal("legacy saved.json was imported again")
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM read_markers`).Scan(&readCount); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM chart_cache`).Scan(&chartCount); err != nil {
		t.Fatal(err)
	}
	if readCount != 0 || chartCount != 0 {
		t.Fatal("legacy plugin JSON was imported again")
	}
}

func TestFeedDBSnapshotCanRestore(t *testing.T) {
	root := t.TempDir()
	livePath := filepath.Join(root, "live", "feed.db")
	backupPath := filepath.Join(root, "sync", "feed.db")
	db, err := openFeedDB(livePath)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := loadFeedCacheDB(db)
	if err != nil {
		t.Fatal(err)
	}
	cache.upsert([]core.Item{{
		App: "x", ID: "1", Title: "first", Source: "@alice",
		VidSecs: 42, Images: []string{"https://img.example/1.jpg"},
	}}, time.Now())
	cache.setSwept(time.Now())
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO read_markers(app,item_id,read_at) VALUES('x','old',123)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO chart_cache(app,name,fetched_at,body) VALUES('douban','weekly','2026-08-27T12:00:00Z','{}')`); err != nil {
		t.Fatal(err)
	}
	if err := db.snapshot(backupPath); err != nil {
		t.Fatal(err)
	}
	cache.upsert([]core.Item{item("x", "2", "second")}, time.Now())
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	if err := db.snapshot(backupPath); err != nil {
		t.Fatal(err)
	}
	if err := db.close(); err != nil {
		t.Fatal(err)
	}

	backup, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var integrity string
	if err := backup.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("snapshot integrity = %q", integrity)
	}
	var count int
	if err := backup.QueryRow(`SELECT count(*) FROM feed_items`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("snapshot has %d feed items, want 2", count)
	}
	for table, want := range map[string]int{"read_markers": 1, "chart_cache": 1} {
		if err := backup.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("snapshot has %d %s rows, want %d", count, table, want)
		}
	}
	var title, source, images string
	var seconds int
	if err := backup.QueryRow(`SELECT title,source,video_seconds,images_json FROM items WHERE app='x' AND id='1'`).Scan(&title, &source, &seconds, &images); err != nil {
		t.Fatal(err)
	}
	if title != "first" || source != "@alice" || seconds != 42 || images != `["https://img.example/1.jpg"]` {
		t.Fatalf("normalized item = %q %q %d %q", title, source, seconds, images)
	}
}

func TestPrepareFeedDBWithoutSyncStaysLocal(t *testing.T) {
	localDir := t.TempDir()
	t.Setenv("TUI_SYNC_DIR", "")
	t.Setenv("XDG_STATE_HOME", localDir)
	db, syncPath, err := prepareFeedDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	if syncPath != "" {
		t.Fatalf("sync path = %q, want none", syncPath)
	}
	if want := filepath.Join(localDir, "tui", "feed.db"); db.path != want {
		t.Fatalf("database path = %q, want %q", db.path, want)
	}
}

func TestFeedDBKeepsItemReferencedBySavedList(t *testing.T) {
	db, err := openFeedDB(filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	cache, err := loadFeedCacheDB(db)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := loadSavedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	it := core.Item{App: "x", ID: "1", Title: "keep me"}
	cache.upsert([]core.Item{it}, now)
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	if err := saved.add(it, now); err != nil {
		t.Fatal(err)
	}
	cache.drop([]string{it.Key()})
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadSavedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	items := reloaded.list(now)
	if len(items) != 1 || items[0].Title != "keep me" {
		t.Fatalf("saved item lost after feed removal: %+v", items)
	}
}

func TestFeedDBKeepsFeedbackAndItsItem(t *testing.T) {
	db, err := openFeedDB(filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.close()
	cache, err := loadFeedCacheDB(db)
	if err != nil {
		t.Fatal(err)
	}
	feedback := loadFeedbackDB(db)
	now := time.Now()
	it := core.Item{App: "reddit", ID: "1", Title: "teach the filter"}
	cache.upsert([]core.Item{it}, now)
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	if err := feedback.set(it, "up", now); err != nil {
		t.Fatal(err)
	}
	cache.drop([]string{it.Key()})
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	got, err := feedback.all()
	if err != nil {
		t.Fatal(err)
	}
	if got[it.Key()] != "up" {
		t.Fatalf("feedback after feed removal = %q, want up", got[it.Key()])
	}
	var title string
	if err := db.db.QueryRow(`SELECT title FROM items WHERE app=? AND id=?`, it.App, it.ID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != it.Title {
		t.Fatalf("labeled item title = %q, want %q", title, it.Title)
	}
}

func TestPrepareFeedDBRestoresSyncedSnapshot(t *testing.T) {
	root := t.TempDir()
	syncDir := filepath.Join(root, "sync")
	localDir := filepath.Join(root, "local")
	t.Setenv("TUI_SYNC_DIR", syncDir)
	t.Setenv("XDG_STATE_HOME", localDir)
	backupPath := filepath.Join(syncDir, "feed.db")
	db, err := openFeedDB(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := loadFeedCacheDB(db)
	if err != nil {
		t.Fatal(err)
	}
	cache.upsert([]core.Item{item("x", "1", "restored")}, time.Now())
	if err := cache.save(); err != nil {
		t.Fatal(err)
	}
	if err := db.close(); err != nil {
		t.Fatal(err)
	}

	restored, gotBackup, err := prepareFeedDB()
	if err != nil {
		t.Fatal(err)
	}
	defer restored.close()
	if gotBackup != backupPath {
		t.Fatalf("backup path = %q, want %q", gotBackup, backupPath)
	}
	cache, err = loadFeedCacheDB(restored)
	if err != nil {
		t.Fatal(err)
	}
	if cache.unreadCount() != 1 {
		t.Fatalf("restored unread count = %d, want 1", cache.unreadCount())
	}
}
