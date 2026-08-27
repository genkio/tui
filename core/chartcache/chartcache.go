package chartcache

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/genkio/tui/core"
)

type Entry struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Body      json.RawMessage `json:"body"`
}

type Cache struct {
	db      *sql.DB
	app     string
	openErr error
	Charts  map[string]Entry `json:"charts"`
}

func Load(path, app, legacyPath string) *Cache {
	c := &Cache{app: app, Charts: map[string]Entry{}}
	db, err := core.OpenFeedDB(path)
	if err != nil {
		c.openErr = err
		return c
	}
	c.db = db
	if legacyPath != "" {
		if err := MigrateJSON(db, app, legacyPath); err != nil {
			c.openErr = err
			return c
		}
	}
	rows, err := db.Query(`SELECT name,fetched_at,body FROM chart_cache WHERE app=?`, app)
	if err != nil {
		c.openErr = err
		return c
	}
	defer rows.Close()
	for rows.Next() {
		var name, stamp string
		var body []byte
		if err := rows.Scan(&name, &stamp, &body); err != nil {
			c.openErr = err
			return c
		}
		at, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			c.openErr = err
			return c
		}
		c.Charts[name] = Entry{FetchedAt: at, Body: body}
	}
	c.openErr = rows.Err()
	return c
}

func (c *Cache) Save() error {
	if c.openErr != nil {
		return c.openErr
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM chart_cache WHERE app=?`, c.app); err != nil {
		return err
	}
	for name, entry := range c.Charts {
		_, err := tx.Exec(`INSERT INTO chart_cache(app,name,fetched_at,body) VALUES(?,?,?,?)`, c.app, name, entry.FetchedAt.UTC().Format(time.RFC3339Nano), []byte(entry.Body))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Cache) Close() error {
	if c.db == nil {
		return nil
	}
	return c.db.Close()
}

func MigrateJSON(db *sql.DB, app, path string) error {
	key := "json_chart_migrated:" + app
	var done string
	err := db.QueryRow(`SELECT value FROM metadata WHERE key=?`, key).Scan(&done)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	charts := map[string]Entry{}
	data, err := os.ReadFile(path)
	if err == nil {
		var legacy struct {
			Charts map[string]Entry `json:"charts"`
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return fmt.Errorf("migrate %s: %w", path, err)
		}
		if legacy.Charts != nil {
			charts = legacy.Charts
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for name, entry := range charts {
		_, err := tx.Exec(`INSERT INTO chart_cache(app,name,fetched_at,body) VALUES(?,?,?,?) ON CONFLICT(app,name) DO UPDATE SET fetched_at=excluded.fetched_at,body=excluded.body`, app, name, entry.FetchedAt.UTC().Format(time.RFC3339Nano), []byte(entry.Body))
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO metadata(key,value) VALUES(?, '1') ON CONFLICT(key) DO NOTHING`, key); err != nil {
		return err
	}
	return tx.Commit()
}
