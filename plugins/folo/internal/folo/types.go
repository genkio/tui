// Package folo is a thin client for Folo's web API (the same endpoints
// app.folo.is calls), authenticated with the browser session cookie. It never
// logs or stores the cookie value.
package folo

import "time"

// Article is one entry from a Folo timeline, flattened from the API's nested
// {entries, feeds} response shape into the fields the UI shows.
type Article struct {
	ID        string // entry id, used to mark read and to fetch the body
	Title     string
	URL       string // original article link, opened by the 'o' key
	Feed      string // source feed title (falls back to its host)
	Author    string
	Published time.Time // publish time; zero if the entry had none
	Age       string    // relative time derived from Published, e.g. "2h"
	Summary   string    // short description from the list response; body fallback
}
