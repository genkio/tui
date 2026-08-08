package main

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

// appColors are the chip colors for the merged feed, matching the terminal
// theme so a service is recognizable at a glance.
var appColors = map[string]string{
	"x":         "#4a9eff", // blue
	"inoreader": "#ffb100", // amber
	"folo":      "#d07df0", // magenta
	"reddit":    "#ff6b33", // orange
	"douban":    "#00b51d", // douban green
}

var appLabels = map[string]string{
	"x":         "𝕏",
	"inoreader": "ino",
	"folo":      "folo",
	"reddit":    "rdt",
	"douban":    "dou",
}

// runWeb serves the merged "all" timeline over HTTP, bound to addr (default
// 0.0.0.0:8080) so other devices on the tailnet can reach it. It reuses the
// same subprocess contract as the terminal all view — each authed app's
// `--json` for the list and `--mark-read` for triage — so read state stays
// consistent between the TUI and the web page. Only the all view is exposed.
// dev turns on the client hot-reload loop: page.tmpl is re-read per request
// and fetched items are cached briefly so refreshes don't re-scrape services.
func runWeb(root, addr string, dev bool) error {
	devPath := ""
	if dev {
		devPath = filepath.Join(root, "cmd", "tui", "page.tmpl")
	}
	loader, err := newPageLoader(devPath)
	if err != nil {
		return err
	}
	var cache *fetchCache
	if dev {
		cache = &fetchCache{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handleAll(w, r, root, loader, cache)
	})
	mux.HandleFunc("/mark", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleMark(w, r, root)
	})

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --web-addr %q: %w", addr, err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind %s: %w", addr, err)
	}
	defer ln.Close()

	fmt.Printf("tui --web serving the all timeline on %s\n", addr)
	if u := tailscaleURL(host, port); u != "" {
		fmt.Printf("  tailnet:  %s\n", u)
	}
	if dev {
		fmt.Printf("  dev: edit %s and refresh — items cached %s between fetches\n", devPath, devCacheTTL)
	}
	fmt.Println("  (ctrl-c to stop)")

	return http.Serve(ln, mux)
}

// tailscaleURL returns the device's Tailscale IPv4 URL for port when tailscale
// is on PATH, so the user has a URL they can open from another device.
func tailscaleURL(host, port string) string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		if out, err := exec.Command(p, "ip", "-4").Output(); err == nil {
			if ip := strings.Fields(string(out)); len(ip) > 0 {
				return "http://" + ip[0] + ":" + port + "/"
			}
		}
	}
	return ""
}

// fetchAllItems runs each authed feed app's `--json` concurrently (the same
// contract the all-view TUI uses) and returns the merged, newest-first items
// plus the names of any apps that failed to load.
func fetchAllItems(ctx context.Context, root string, apps []string, xTab string, now time.Time) ([]core.Item, []string, string) {
	var (
		mu     sync.Mutex
		all    []core.Item
		failed []string
		warn   string
		wg     sync.WaitGroup
	)
	for _, app := range apps {
		wg.Add(1)
		go func(app string) {
			defer wg.Done()
			appCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			args := []string{app, "--json"}
			if app == "x" {
				args = append(args, "--tab", xTab) // For You / Following
			}
			cmd := exec.CommandContext(appCtx, self(), args...)
			cmd.Env = appEnv(filepath.Join(root, "plugins", app))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				mu.Lock()
				// every plugin emits this marker for an expired session
				if bytes.Contains(stderr.Bytes(), []byte("session is stale")) {
					warn = strings.TrimSpace(warn + " " + app + " session is stale — re-run `tui " + app + " --auth`.")
				}
				failed = append(failed, app)
				mu.Unlock()
				return
			}
			items, perr := core.ParseItems(out, now)
			if perr != nil {
				mu.Lock()
				failed = append(failed, app)
				mu.Unlock()
				return
			}
			mu.Lock()
			all = append(all, items...)
			mu.Unlock()
		}(app)
	}
	wg.Wait()
	core.MergeSort(all)
	return all, failed, warn
}

// authedFeedApps lists the logged-in apps the all view merges, freshly computed
// so a login picked up while the server runs is reflected on the next load.
func authedFeedApps(root string) []string {
	var out []string
	for _, a := range appsIn(root) {
		if a.feed && a.authed() {
			out = append(out, a.name)
		}
	}
	return out
}

// devCacheTTL is how long --dev reuses fetched items between page loads, so a
// style-tweak refresh loop doesn't re-scrape every service.
const devCacheTTL = 60 * time.Second

// fetchCache remembers the last fetch per x tab; --dev only.
type fetchCache struct {
	mu     sync.Mutex
	xTab   string
	at     time.Time
	items  []core.Item
	failed []string
	warn   string
}

func (c *fetchCache) get(xTab string, now time.Time) ([]core.Item, []string, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.xTab != xTab || c.at.IsZero() || now.Sub(c.at) > devCacheTTL {
		return nil, nil, "", false
	}
	// copy: callers sort in place, and concurrent requests share this cache
	return append([]core.Item(nil), c.items...), c.failed, c.warn, true
}

func (c *fetchCache) put(xTab string, now time.Time, items []core.Item, failed []string, warn string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xTab, c.at, c.items, c.failed, c.warn = xTab, now, items, failed, warn
}

// handleAll renders the all timeline as a mobile-friendly HTML page (or JSON
// with ?json=1). Default order is oldest-first; ?order=desc flips to newest-first.
// ?x=foryou serves x's For You timeline instead of the Following default (used
// only ephemerally; the page resets to following on reload).
func handleAll(w http.ResponseWriter, r *http.Request, root string, loader *pageLoader, cache *fetchCache) {
	// In --dev a template typo should show up immediately, not after a full
	// fetch, so load (and in dev, re-parse) the template first.
	tmpl, err := loader.load()
	if err != nil {
		http.Error(w, "page template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	apps := authedFeedApps(root)

	xTab := r.URL.Query().Get("x")
	if xTab != "foryou" {
		xTab = "following"
	}

	var items []core.Item
	var failed []string
	var warn string
	if len(apps) > 0 {
		cached := false
		if cache != nil {
			items, failed, warn, cached = cache.get(xTab, now)
		}
		if !cached {
			fetchCtx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
			defer cancel()
			items, failed, warn = fetchAllItems(fetchCtx, root, apps, xTab, now)
			if cache != nil {
				cache.put(xTab, now, items, failed, warn)
			}
		}
	}
	asc := r.URL.Query().Get("order") != "desc" // oldest first by default
	sortItems(items, asc)

	if r.URL.Query().Get("json") == "1" {
		w.Header().Set("Content-Type", "application/json")
		writeJSONItems(w, items, failed)
		return
	}

	// Render to a buffer first so a template error mid-page becomes a clean
	// 500 instead of half a document.
	var page bytes.Buffer
	if err := tmpl.Execute(&page, buildPageData(items, apps, failed, now, asc, xTab, warn)); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page.Bytes())
}

// sortItems orders the feed by publish time: oldest first when asc is true,
// newest first otherwise. Items without a resolvable time sink to the bottom.
func sortItems(items []core.Item, asc bool) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].At, items[j].At
		switch {
		case a.IsZero() && b.IsZero():
			return false
		case a.IsZero():
			return false
		case b.IsZero():
			return true
		default:
			if asc {
				return a.Before(b)
			}
			return a.After(b)
		}
	})
}

// handleMark marks one or more items read and, unless it asked for JSON,
// redirects back to the page, reusing each app's `--mark-read` so the change is
// consistent with the TUI's read state.
func handleMark(w http.ResponseWriter, r *http.Request, root string) {
	app := r.FormValue("app")
	ids := r.Form["id"] // one or many 'id' values
	clean := ids[:0]
	for _, id := range ids {
		if id != "" {
			clean = append(clean, id)
		}
	}
	if app == "" || len(clean) == 0 {
		http.Error(w, "missing app or id", http.StatusBadRequest)
		return
	}
	if err := runMarkRead(root, app, clean, 30*time.Second); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.FormValue("json") == "1" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true}`)
		return
	}
	// Referer-safe: go back where we came from, else the home page.
	back := r.Referer()
	if back == "" || !strings.HasPrefix(back, "/") {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// escape is a shorthand for html.EscapeString in templates below.
func escape(s string) string { return html.EscapeString(s) }
