package readstore

import (
	"path/filepath"
	"testing"
)

func TestMarkHasUnmark(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "feed.db"))
	defer s.Close()
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
	path := filepath.Join(t.TempDir(), "feed.db")
	s := Load(path)
	defer s.Close()
	s.Mark("100")
	s.Mark("200")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reopened := Load(path)
	defer reopened.Close()
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
	s := Load(filepath.Join(t.TempDir(), "missing", "feed.db"))
	defer s.Close()
	if s.Has("anything") {
		t.Fatal("missing file should load as empty, not error")
	}
	s.Mark("1")
	if err := s.Save(); err != nil {
		t.Fatalf("Save into missing dir: %v", err)
	}
}
