// Package inoreader is a thin client for Inoreader's web ("xajax") API, the
// same endpoints the website itself calls, authenticated with the browser
// session cookie. It never logs or stores the cookie value.
package inoreader

// Article is one item from the article stream, scraped from the HTML fragment
// the web app returns.
type Article struct {
	ID      string // numeric Inoreader article id, used to mark read
	Title   string
	URL     string // article link, opened by the 'o' key
	Feed    string // source feed title, e.g. "Hacker News: Best"
	Author  string
	Age     string // server-rendered relative time, e.g. "2h"; "" if absent
	Content string // body flattened to plain text; shown on expand
}
