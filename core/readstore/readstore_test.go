package readstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/genkio/tui/core"
)

func TestMigrateJSON(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "read.json")
	data, err := json.Marshal(map[string]any{"read": map[string]int64{"old": 123}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, data, 0o644); err != nil {
		t.Fatal(err)
	}
	store := Load(filepath.Join(dir, "feed.db"), "x", legacy)
	defer store.Close()
	if !store.Has("old") {
		t.Fatal("legacy marker was not imported")
	}
	store.Unmark("old")
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	reopened := Load(filepath.Join(dir, "feed.db"), "x", legacy)
	defer reopened.Close()
	if reopened.Has("old") {
		t.Fatal("legacy JSON was imported again")
	}
}

func TestConcurrentStoresMergeDeltas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.db")
	a := Load(path, "x", "")
	defer a.Close()
	b := Load(path, "x", "")
	defer b.Close()
	a.Mark("a")
	b.Mark("b")
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	got := Load(path, "x", "")
	defer got.Close()
	if !got.Has("a") || !got.Has("b") {
		t.Fatal("one writer overwrote the other's marker")
	}
}

func TestStoreRetainsRowsPastFormerLimit(t *testing.T) {
	const formerLimit = 20000
	path := filepath.Join(t.TempDir(), "feed.db")
	db, err := core.OpenFeedDB(path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < formerLimit+50; i++ {
		if _, err := tx.Exec(`INSERT INTO read_markers(app,item_id,read_at) VALUES('x',?,?)`, "id-"+strconv.Itoa(i), i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store := Load(path, "x", "")
	defer store.Close()
	store.Mark("newest")
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM read_markers WHERE app='x'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != formerLimit+51 {
		t.Fatalf("store kept %d, want %d", count, formerLimit+51)
	}
	if !store.Has("id-0") {
		t.Fatal("oldest marker was deleted")
	}
}
