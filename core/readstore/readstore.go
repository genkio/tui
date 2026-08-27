package readstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

type Store struct {
	mu       sync.Mutex
	db       *sql.DB
	app      string
	path     string
	openErr  error
	ids      map[string]int64
	marked   map[string]int64
	unmarked map[string]bool
}

func Load(path, app, legacyPath string) *Store {
	if path == "" {
		path = core.FeedDBPath()
	}
	s := &Store{
		app: app, path: path, ids: map[string]int64{},
		marked: map[string]int64{}, unmarked: map[string]bool{},
	}
	db, err := core.OpenFeedDB(path)
	if err != nil {
		s.openErr = err
		return s
	}
	s.db = db
	if legacyPath != "" {
		if err := MigrateJSON(db, app, legacyPath); err != nil {
			s.openErr = err
			return s
		}
	}
	rows, err := db.Query(`SELECT item_id,read_at FROM read_markers WHERE app=?`, app)
	if err != nil {
		s.openErr = err
		return s
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var at int64
		if err := rows.Scan(&id, &at); err != nil {
			s.openErr = err
			return s
		}
		s.ids[id] = at
	}
	s.openErr = rows.Err()
	return s
}

func (s *Store) Path() string { return s.path }

func (s *Store) Has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ids[id]
	return ok
}

func (s *Store) Mark(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	at := time.Now().Unix()
	s.ids[id] = at
	s.marked[id] = at
	delete(s.unmarked, id)
	s.mu.Unlock()
}

func (s *Store) Unmark(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	delete(s.ids, id)
	delete(s.marked, id)
	s.unmarked[id] = true
	s.mu.Unlock()
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openErr != nil {
		return s.openErr
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, at := range s.marked {
		if _, err := tx.Exec(`INSERT INTO read_markers(app,item_id,read_at) VALUES(?,?,?) ON CONFLICT(app,item_id) DO UPDATE SET read_at=excluded.read_at`, s.app, id, at); err != nil {
			return err
		}
	}
	for id := range s.unmarked {
		if _, err := tx.Exec(`DELETE FROM read_markers WHERE app=? AND item_id=?`, s.app, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.marked = map[string]int64{}
	s.unmarked = map[string]bool{}
	return nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func MigrateJSON(db *sql.DB, app, path string) error {
	key := "json_read_migrated:" + app
	var done string
	err := db.QueryRow(`SELECT value FROM metadata WHERE key=?`, key).Scan(&done)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	read := map[string]int64{}
	data, err := os.ReadFile(path)
	if err == nil {
		var legacy struct {
			Read map[string]int64 `json:"read"`
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return fmt.Errorf("migrate %s: %w", path, err)
		}
		if legacy.Read != nil {
			read = legacy.Read
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, at := range read {
		if _, err := tx.Exec(`INSERT INTO read_markers(app,item_id,read_at) VALUES(?,?,?) ON CONFLICT(app,item_id) DO UPDATE SET read_at=excluded.read_at`, app, id, at); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO metadata(key,value) VALUES(?, '1') ON CONFLICT(key) DO NOTHING`, key); err != nil {
		return err
	}
	return tx.Commit()
}
