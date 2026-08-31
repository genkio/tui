package core

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func FeedDBPath() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "tui", "feed.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "tui", "feed.db")
}

func OpenFeedDB(path string) (*sql.DB, error) {
	if path == "" {
		path = FeedDBPath()
	}
	if path == "" {
		return nil, errors.New("no feed database path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(feedSchema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

const feedSchema = `
PRAGMA busy_timeout = 5000;
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS items (
  app TEXT NOT NULL,
  id TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  age TEXT NOT NULL DEFAULT '',
  published_at TEXT NOT NULL DEFAULT '',
  video TEXT NOT NULL DEFAULT '',
  poster TEXT NOT NULL DEFAULT '',
  video_seconds INTEGER NOT NULL DEFAULT 0,
  audio TEXT NOT NULL DEFAULT '',
  images_json TEXT NOT NULL DEFAULT 'null',
  quote_json TEXT NOT NULL DEFAULT 'null',
  PRIMARY KEY (app, id)
);
CREATE TABLE IF NOT EXISTS feed_items (
  app TEXT NOT NULL,
  id TEXT NOT NULL,
  first_seen TEXT NOT NULL DEFAULT '',
  read INTEGER NOT NULL DEFAULT 0,
  read_at TEXT NOT NULL DEFAULT '',
  synced INTEGER NOT NULL DEFAULT 0,
  ordinal INTEGER NOT NULL,
  PRIMARY KEY (app, id),
  FOREIGN KEY (app, id) REFERENCES items(app, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS feed_items_unread ON feed_items(read, app);
CREATE TABLE IF NOT EXISTS app_status (
  app TEXT PRIMARY KEY,
  at TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  stale INTEGER NOT NULL DEFAULT 0,
  capped INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS saved_items (
  app TEXT NOT NULL,
  id TEXT NOT NULL,
  saved_at TEXT NOT NULL DEFAULT '',
  pos REAL NOT NULL DEFAULT 0,
  pos_src TEXT NOT NULL DEFAULT '',
  ordinal INTEGER NOT NULL,
  PRIMARY KEY (app, id),
  FOREIGN KEY (app, id) REFERENCES items(app, id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS item_tags (
  app TEXT NOT NULL,
  id TEXT NOT NULL,
  tag TEXT NOT NULL,
  tagged_at TEXT NOT NULL,
  PRIMARY KEY (app, id, tag),
  FOREIGN KEY (app, id) REFERENCES items(app, id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS keywords (
  keyword TEXT PRIMARY KEY COLLATE NOCASE,
  ordinal INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS blocked_items (
  app TEXT NOT NULL,
  id TEXT NOT NULL,
  blocked_at TEXT NOT NULL DEFAULT '',
  keyword TEXT NOT NULL DEFAULT '',
  ordinal INTEGER NOT NULL,
  PRIMARY KEY (app, id),
  FOREIGN KEY (app, id) REFERENCES items(app, id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS read_markers (
  app TEXT NOT NULL,
  item_id TEXT NOT NULL,
  read_at INTEGER NOT NULL,
  PRIMARY KEY (app, item_id)
);
CREATE INDEX IF NOT EXISTS read_markers_recent ON read_markers(app, read_at DESC);
CREATE TABLE IF NOT EXISTS chart_cache (
  app TEXT NOT NULL,
  name TEXT NOT NULL,
  fetched_at TEXT NOT NULL,
  body BLOB NOT NULL,
  PRIMARY KEY (app, name)
);
CREATE TABLE IF NOT EXISTS metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
DROP TABLE IF EXISTS item_feedback;
PRAGMA user_version = 1;`
