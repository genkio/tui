package readstore

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestMarkHasUnmark(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "read.json"))
	if s.Has("1") {
		t.Fatal("fresh store should not know id 1")
	}
	s.Mark("1")
	if !s.Has("1") {
		t.Fatal("id 1 should be read after Mark")
	}
	s.Unmark("1")
	if s.Has("1") {
		t.Fatal("id 1 should be unread after Unmark")
	}
	s.Mark("")
	if s.Has("") {
		t.Fatal("empty id should never be stored")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read.json")
	s := Load(path)
	s.Mark("100")
	s.Mark("200")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reopened := Load(path)
	for _, id := range []string{"100", "200"} {
		if !reopened.Has(id) {
			t.Fatalf("reopened store lost id %s", id)
		}
	}
	if reopened.Has("300") {
		t.Fatal("reopened store invented id 300")
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if s.Has("anything") {
		t.Fatal("missing file should load as empty, not error")
	}
	// Marking then saving must create the file and its parent dir.
	s.Mark("1")
	if err := s.Save(); err != nil {
		t.Fatalf("Save into missing dir: %v", err)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "read.json"))
	// Seed just over the cap with ascending timestamps so newest is deterministic.
	for i := 0; i < maxEntries+50; i++ {
		s.ids[id(i)] = int64(i)
	}
	s.prune()
	if len(s.ids) != maxEntries {
		t.Fatalf("prune kept %d, want %d", len(s.ids), maxEntries)
	}
	if _, ok := s.ids[id(0)]; ok {
		t.Fatal("prune should have dropped the oldest marker")
	}
	if _, ok := s.ids[id(maxEntries+49)]; !ok {
		t.Fatal("prune should have kept the newest marker")
	}
}

func id(i int) string {
	return "id-" + strconv.Itoa(i)
}
