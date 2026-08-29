package main

import (
	"errors"
	"time"

	"github.com/genkio/tui/core"
)

var savedTagOptions = []string{"later", "useful", "list", "fun", "nsfw"}

var errTagItemNotSaved = errors.New("item is no longer saved")

type tagStore struct {
	db *feedDB
}

func loadTagsDB(db *feedDB) *tagStore {
	return &tagStore{db: db}
}

func validSavedTag(tag string) bool {
	for _, option := range savedTagOptions {
		if tag == option {
			return true
		}
	}
	return false
}

func (s *tagStore) all() (map[string][]string, error) {
	rows, err := s.db.db.Query(`SELECT app,id,tag FROM item_tags ORDER BY tagged_at,tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var app, id, tag string
		if err := rows.Scan(&app, &id, &tag); err != nil {
			return nil, err
		}
		key := core.Key(app, id)
		out[key] = append(out[key], tag)
	}
	return out, rows.Err()
}

func (s *tagStore) set(app, id, tag string, on bool, now time.Time) error {
	if !on {
		_, err := s.db.db.Exec(`DELETE FROM item_tags WHERE app=? AND id=? AND tag=?`, app, id, tag)
		return err
	}
	result, err := s.db.db.Exec(`INSERT INTO item_tags(app,id,tag,tagged_at)
SELECT app,id,?,? FROM saved_items WHERE app=? AND id=?
ON CONFLICT(app,id,tag) DO UPDATE SET tagged_at=excluded.tagged_at`,
		tag, now.UTC().Format(time.RFC3339), app, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return errTagItemNotSaved
	}
	return nil
}

func itemHasTag(tags map[string][]string, key, tag string) bool {
	for _, current := range tags[key] {
		if current == tag {
			return true
		}
	}
	return false
}
