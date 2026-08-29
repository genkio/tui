package main

import (
	"time"

	"github.com/genkio/tui/core"
)

type feedbackStore struct {
	db *feedDB
}

func loadFeedbackDB(db *feedDB) *feedbackStore {
	return &feedbackStore{db: db}
}

func (s *feedbackStore) all() (map[string]string, error) {
	rows, err := s.db.db.Query(`SELECT app,id,feedback FROM item_feedback`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var app, id, feedback string
		if err := rows.Scan(&app, &id, &feedback); err != nil {
			return nil, err
		}
		out[core.Key(app, id)] = feedback
	}
	return out, rows.Err()
}

func (s *feedbackStore) set(it core.Item, feedback string, now time.Time) error {
	tx, err := s.db.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeItem(tx, it.Wire(), true); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO item_feedback(app,id,feedback,feedback_at) VALUES(?,?,?,?)
ON CONFLICT(app,id) DO UPDATE SET feedback=excluded.feedback,feedback_at=excluded.feedback_at`,
		it.App, it.ID, feedback, now.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	return tx.Commit()
}
