package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/core/chartcache"
	sharedread "github.com/genkio/tui/core/readstore"
)

type feedDB struct {
	db   *sql.DB
	path string
}

func openFeedDB(path string) (*feedDB, error) {
	db, err := core.OpenFeedDB(path)
	if err != nil {
		return nil, err
	}
	return &feedDB{db: db, path: path}, nil
}

func readFeedCacheDB(path string) (*feedCache, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	store := &feedDB{db: db, path: path}
	cache, err := loadFeedCacheDB(store)
	if err != nil {
		return nil, err
	}
	cache.db = nil
	return cache, nil
}

func (s *feedDB) close() error { return s.db.Close() }

func (s *feedDB) migrateJSON() error {
	var done string
	err := s.db.QueryRow(`SELECT value FROM metadata WHERE key = 'json_migrated'`).Scan(&done)
	if err == nil {
		return s.migratePluginJSON()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if data, err := os.ReadFile(core.StatePath("tui", "feed.json")); err == nil {
		var f feedFile
		if json.Unmarshal(data, &f) == nil {
			if err := s.replaceFeed(f); err != nil {
				return err
			}
		}
	}
	if data, err := os.ReadFile(core.StatePath("tui", "saved.json")); err == nil {
		var f savedFile
		if json.Unmarshal(data, &f) == nil {
			if err := s.replaceSaved(f.Items); err != nil {
				return err
			}
		}
	}
	var words []string
	if data, err := os.ReadFile(core.StatePath("tui", "keywords.json")); err == nil {
		var f keywordFile
		if json.Unmarshal(data, &f) == nil {
			words = f.Keywords
		}
	}
	var blocked []blockedItem
	if data, err := os.ReadFile(core.StatePath("tui", "blocked.json")); err == nil {
		var f blockedFile
		if json.Unmarshal(data, &f) == nil {
			blocked = f.Items
		}
	}
	if err := s.replaceBlocker(words, blocked); err != nil {
		return err
	}
	if _, err = s.db.Exec(`INSERT INTO metadata(key, value) VALUES('json_migrated', '1')`); err != nil {
		return err
	}
	return s.migratePluginJSON()
}

func (s *feedDB) migratePluginJSON() error {
	readStores := []struct {
		app  string
		path string
	}{
		{"x", core.StatePath("x-tui", "read.json")},
		{"reddit", core.StatePath("reddit-tui", "read.json")},
		{"douban", core.StatePath("douban-tui", "read.json")},
		{"bilibili", core.StatePath("bilibili-tui", "read.json")},
	}
	for _, store := range readStores {
		if err := sharedread.MigrateJSON(s.db, store.app, store.path); err != nil {
			return err
		}
	}
	return chartcache.MigrateJSON(s.db, "douban", core.StatePath("douban-tui", "charts.json"))
}

const itemColumns = `i.app,i.id,i.title,i.body,i.source,i.author,i.url,i.age,i.published_at,i.video,i.poster,i.video_seconds,i.audio,i.images_json,i.quote_json`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWire(row rowScanner, wire *core.Wire, extra ...any) error {
	var images, quote string
	dest := []any{
		&wire.App, &wire.ID, &wire.Title, &wire.Body, &wire.Source,
		&wire.Author, &wire.URL, &wire.Age, &wire.TS, &wire.Video,
		&wire.Poster, &wire.VidSecs, &wire.Audio, &images, &quote,
	}
	dest = append(dest, extra...)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(images), &wire.Images); err != nil {
		return err
	}
	return json.Unmarshal([]byte(quote), &wire.Quote)
}

func writeItem(tx *sql.Tx, wire core.Wire, update bool) error {
	images, err := json.Marshal(wire.Images)
	if err != nil {
		return err
	}
	quote, err := json.Marshal(wire.Quote)
	if err != nil {
		return err
	}
	query := `INSERT INTO items(app,id,title,body,source,author,url,age,published_at,video,poster,video_seconds,audio,images_json,quote_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(app,id) DO NOTHING`
	if update {
		query = `INSERT INTO items(app,id,title,body,source,author,url,age,published_at,video,poster,video_seconds,audio,images_json,quote_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(app,id) DO UPDATE SET title=excluded.title,body=excluded.body,source=excluded.source,author=excluded.author,url=excluded.url,age=excluded.age,published_at=excluded.published_at,video=excluded.video,poster=excluded.poster,video_seconds=excluded.video_seconds,audio=excluded.audio,images_json=excluded.images_json,quote_json=excluded.quote_json`
	}
	_, err = tx.Exec(query,
		wire.App, wire.ID, wire.Title, wire.Body, wire.Source, wire.Author, wire.URL,
		wire.Age, wire.TS, wire.Video, wire.Poster, wire.VidSecs, wire.Audio, images, quote)
	return err
}

func (s *feedDB) putItem(wire core.Wire) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeItem(tx, wire, true); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteUnreferencedItems(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM items
WHERE NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.app=items.app AND feed_items.id=items.id)
  AND NOT EXISTS (SELECT 1 FROM saved_items WHERE saved_items.app=items.app AND saved_items.id=items.id)
  AND NOT EXISTS (SELECT 1 FROM blocked_items WHERE blocked_items.app=items.app AND blocked_items.id=items.id)`)
	return err
}

func (s *feedDB) loadFeed() (feedFile, error) {
	f := feedFile{Status: map[string]appStatus{}}
	rows, err := s.db.Query(`SELECT ` + itemColumns + `,f.first_seen,f.read,f.read_at,f.synced
FROM feed_items f JOIN items i ON i.app=f.app AND i.id=f.id ORDER BY f.ordinal`)
	if err != nil {
		return f, err
	}
	defer rows.Close()
	for rows.Next() {
		var e feedEntry
		if err := scanWire(rows, &e.Wire, &e.FirstSeen, &e.Read, &e.ReadAt, &e.Synced); err != nil {
			return f, err
		}
		f.Items = append(f.Items, &e)
	}
	if err := rows.Err(); err != nil {
		return f, err
	}
	rows, err = s.db.Query(`SELECT app, at, error, stale, capped FROM app_status`)
	if err != nil {
		return f, err
	}
	defer rows.Close()
	for rows.Next() {
		var app string
		var st appStatus
		if err := rows.Scan(&app, &st.At, &st.Err, &st.Stale, &st.Capped); err != nil {
			return f, err
		}
		f.Status[app] = st
	}
	if err := rows.Err(); err != nil {
		return f, err
	}
	_ = s.db.QueryRow(`SELECT value FROM metadata WHERE key = 'swept'`).Scan(&f.Swept)
	return f, nil
}

func (s *feedDB) replaceFeed(f feedFile) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM feed_items`); err != nil {
		return err
	}
	for i, e := range f.Items {
		if e == nil {
			continue
		}
		if err := writeItem(tx, e.Wire, true); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO feed_items(app,id,first_seen,read,read_at,synced,ordinal) VALUES(?,?,?,?,?,?,?)`, e.App, e.ID, e.FirstSeen, e.Read, e.ReadAt, e.Synced, i)
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM app_status`); err != nil {
		return err
	}
	for app, st := range f.Status {
		_, err := tx.Exec(`INSERT INTO app_status(app,at,error,stale,capped) VALUES(?,?,?,?,?)`, app, st.At, st.Err, st.Stale, st.Capped)
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO metadata(key,value) VALUES('swept',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, f.Swept); err != nil {
		return err
	}
	if err := deleteUnreferencedItems(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *feedDB) loadSaved() ([]savedItem, error) {
	rows, err := s.db.Query(`SELECT ` + itemColumns + `,s.saved_at,s.pos,s.pos_src
FROM saved_items s JOIN items i ON i.app=s.app AND i.id=s.id ORDER BY s.ordinal`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []savedItem
	for rows.Next() {
		var item savedItem
		if err := scanWire(rows, &item.Wire, &item.SavedAt, &item.Pos, &item.PosSrc); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *feedDB) replaceSaved(items []savedItem) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM saved_items`); err != nil {
		return err
	}
	for i, item := range items {
		if err := writeItem(tx, item.Wire, false); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO saved_items(app,id,saved_at,pos,pos_src,ordinal) VALUES(?,?,?,?,?,?)`, item.App, item.ID, item.SavedAt, item.Pos, item.PosSrc, i)
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM item_tags
WHERE NOT EXISTS (SELECT 1 FROM saved_items WHERE saved_items.app=item_tags.app AND saved_items.id=item_tags.id)`); err != nil {
		return err
	}
	if err := deleteUnreferencedItems(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *feedDB) loadBlocker() ([]string, []blockedItem, error) {
	rows, err := s.db.Query(`SELECT keyword FROM keywords ORDER BY ordinal`)
	if err != nil {
		return nil, nil, err
	}
	var words []string
	for rows.Next() {
		var word string
		if err := rows.Scan(&word); err != nil {
			rows.Close()
			return nil, nil, err
		}
		words = append(words, word)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	rows, err = s.db.Query(`SELECT ` + itemColumns + `,b.blocked_at,b.keyword
FROM blocked_items b JOIN items i ON i.app=b.app AND i.id=b.id ORDER BY b.ordinal`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var items []blockedItem
	for rows.Next() {
		var item blockedItem
		if err := scanWire(rows, &item.Wire, &item.BlockedAt, &item.Keyword); err != nil {
			return nil, nil, err
		}
		items = append(items, item)
	}
	return words, items, rows.Err()
}

func (s *feedDB) replaceBlocker(words []string, items []blockedItem) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM keywords`); err != nil {
		return err
	}
	for i, word := range words {
		if _, err := tx.Exec(`INSERT INTO keywords(keyword,ordinal) VALUES(?,?)`, word, i); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM blocked_items`); err != nil {
		return err
	}
	for i, item := range items {
		if err := writeItem(tx, item.Wire, false); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO blocked_items(app,id,blocked_at,keyword,ordinal) VALUES(?,?,?,?,?)`, item.App, item.ID, item.BlockedAt, item.Keyword, i)
		if err != nil {
			return err
		}
	}
	if err := deleteUnreferencedItems(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *feedDB) snapshot(path string) error {
	if path == "" || path == s.path {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := s.db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func prepareFeedDB() (*feedDB, string, error) {
	live := core.FeedDBPath()
	syncPath := ""
	if core.SyncDir() != "" {
		syncPath = filepath.Join(core.SyncDir(), "feed.db")
	}
	if syncPath != "" {
		if _, err := os.Stat(live); errors.Is(err, os.ErrNotExist) {
			if err := copyFile(syncPath, live); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, "", fmt.Errorf("restore feed database: %w", err)
			}
		}
	}
	store, err := openFeedDB(live)
	if err != nil {
		return nil, "", err
	}
	if err := store.migrateJSON(); err != nil {
		store.close()
		return nil, "", fmt.Errorf("migrate JSON state: %w", err)
	}
	return store, syncPath, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".restore-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dst)
}
