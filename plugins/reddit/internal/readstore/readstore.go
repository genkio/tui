package readstore

import (
	"github.com/genkio/tui/core"
	shared "github.com/genkio/tui/core/readstore"
)

type Store = shared.Store

func Load(path string) *Store {
	legacy := ""
	if path == "" {
		legacy = core.StatePath("reddit-tui", "read.json")
	}
	return shared.Load(path, "reddit", legacy)
}

func DefaultPath() string { return core.FeedDBPath() }
