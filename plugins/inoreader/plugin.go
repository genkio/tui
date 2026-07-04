// Package inoreader runs the Inoreader unread-triage TUI: it reads the "All
// articles" stream oldest-first, expands items inline, opens them in the
// browser, and marks them read via your session cookie. The tui launcher
// dispatches `tui inoreader` to Main; the standalone binary wraps it too.
package inoreader

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/inoreader/internal/config"
	"github.com/genkio/tui/plugins/inoreader/internal/inoreader"
	"github.com/genkio/tui/plugins/inoreader/internal/ui"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func Main() int {
	core.LoadUserEnv()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inoreader-tui: "+err.Error())
		return 1
	}
	return 0
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "verify the connection to Inoreader and exit")
		count       = flag.Bool("count", false, "print the unread article count and exit")
		dumpJSON    = flag.Bool("json", false, "print unread articles as JSON and exit (for the 'all' timeline)")
		markRead    = flag.Bool("mark-read", false, "mark read the article ids read from stdin (one per line) and exit")
		auth        = flag.Bool("auth", false, "log in via a browser and capture the session into ~/.config/tui/env")
		configPath  = flag.String("config", "", "config file path (default: $XDG_CONFIG_HOME/inoreader-tui/config.toml)")
		refresh     = flag.Duration("refresh", 0, "auto-refresh the unread list at this interval (e.g. 5m); off if unset")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("inoreader-tui " + versionString())
		return nil
	}
	if *auth {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return core.RunAuth(ctx, "https://www.inoreader.com/all_articles", func(s *core.Session) (map[string]string, error) {
			cookie := s.CookieHeader("inoreader.com")
			if cookie == "" {
				return nil, errors.New("no inoreader.com cookies captured; were you fully logged in?")
			}
			return map[string]string{"INOREADER_COOKIE": cookie}, nil
		})
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := config.ValidateAuth(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := inoreader.New(cfg.BaseURL, config.Cookie(), cfg.UserAgent)

	if *check {
		return printCheck(ctx, client, cfg)
	}
	if *count {
		return printCount(ctx, client, cfg)
	}
	if *dumpJSON {
		return printJSON(ctx, client, cfg)
	}
	if *markRead {
		return markReadFromStdin(ctx, client)
	}

	interval := cfg.RefreshInterval()
	if *refresh > 0 { // an explicit flag wins over the config/env value
		interval = *refresh
	}
	return ui.Run(ctx, client, cfg, interval)
}

// printCheck probes the API and reports whether reading will work.
func printCheck(ctx context.Context, client *inoreader.Client, cfg config.Config) error {
	fmt.Println("inoreader-tui " + versionString())
	fmt.Println("\nEndpoint: " + cfg.BaseURL)
	mode := "all articles"
	if cfg.UnreadOnly {
		mode = "unread only"
	}
	fmt.Println("Mode:     " + mode + ", oldest first")

	fmt.Println("\nReadiness:")
	arts, err := client.Unreads(ctx, cfg.UnreadOnly, 5)
	if err != nil {
		fmt.Printf("  [--] read articles: %s\n", err.Error())
		return nil
	}
	fmt.Printf("  [ok] read articles (%d fetched)\n", len(arts))
	if len(arts) > 0 {
		fmt.Printf("       oldest: %q (%s)\n", arts[0].Title, arts[0].Feed)
	} else {
		fmt.Println("       nothing to show right now")
	}
	fmt.Println("  [ok] mark as read uses the same session (press r in the TUI)")
	return nil
}

// printCount prints the unread article count for the launcher's badge. It uses
// the same fetch cap as the TUI (MaxArticles) so the badge matches what you see
// inside; a capped result is reported as "N+".
func printCount(ctx context.Context, client *inoreader.Client, cfg config.Config) error {
	n, capped, err := inoreader.NewSource(client, true, cfg.MaxArticles).Count(ctx)
	if err != nil {
		return err
	}
	suffix := ""
	if capped {
		suffix = "+"
	}
	fmt.Printf("%d%s\n", n, suffix)
	return nil
}

// dumpItem is the normalized shape the launcher's "all" timeline consumes from
// every app's --json output.
type dumpItem struct {
	App    string `json:"app"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Source string `json:"source,omitempty"`
	Author string `json:"author,omitempty"`
	URL    string `json:"url,omitempty"`
	Age    string `json:"age,omitempty"`
	TS     string `json:"ts,omitempty"`
}

// printJSON dumps the unread articles as a JSON array for the launcher's "all"
// view. Inoreader gives no absolute publish time (only a relative age string),
// so ts is left empty and the launcher derives a sort key from age.
func printJSON(ctx context.Context, client *inoreader.Client, cfg config.Config) error {
	arts, err := client.Unreads(ctx, true, cfg.MaxArticles)
	if err != nil {
		return err
	}
	items := make([]dumpItem, 0, len(arts))
	for _, a := range arts {
		items = append(items, dumpItem{
			App:    "inoreader",
			ID:     a.ID,
			Title:  a.Title,
			Body:   a.Content,
			Source: a.Feed,
			Author: a.Author,
			URL:    a.URL,
			Age:    a.Age,
		})
	}
	return json.NewEncoder(os.Stdout).Encode(items)
}

// markReadFromStdin marks read every article id on stdin (one per line) via the
// same session the standalone app uses, so posts triaged in the "all" view drop
// out of Inoreader's unread stream everywhere.
func markReadFromStdin(ctx context.Context, client *inoreader.Client) error {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var firstErr error
	for sc.Scan() {
		id := strings.TrimSpace(sc.Text())
		if id == "" {
			continue
		}
		if err := client.MarkRead(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return firstErr
}

func versionString() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}
