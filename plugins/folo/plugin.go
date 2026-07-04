// Package folo runs the Folo pending-article triage TUI: it reads the Articles
// timeline, expands items inline, opens them in the browser, and marks them
// read via your session cookie. The tui launcher dispatches `tui folo` to Main;
// the standalone binary wraps it too.
package folo

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"time"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/folo/internal/config"
	"github.com/genkio/tui/plugins/folo/internal/folo"
	"github.com/genkio/tui/plugins/folo/internal/ui"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func Main() int {
	core.LoadUserEnv()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "folo-tui: "+err.Error())
		return 1
	}
	return 0
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "verify the connection to Folo and exit")
		count       = flag.Bool("count", false, "print the pending (unread) article count and exit")
		dumpJSON    = flag.Bool("json", false, "print pending articles as JSON and exit (for the 'all' timeline)")
		markRead    = flag.Bool("mark-read", false, "mark read the entry ids read from stdin (one per line) and exit")
		configPath  = flag.String("config", "", "config file path (default: $XDG_CONFIG_HOME/folo-tui/config.toml)")
		refresh     = flag.Duration("refresh", 0, "auto-refresh the list at this interval (e.g. 5m); off if unset")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("folo-tui " + versionString())
		return nil
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

	client := folo.New(cfg.BaseURL, cfg.WebURL, config.Cookie(), cfg.UserAgent)

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
func printCheck(ctx context.Context, client *folo.Client, cfg config.Config) error {
	fmt.Println("folo-tui " + versionString())
	fmt.Println("\nEndpoint: " + cfg.BaseURL)
	mode := "all articles"
	if cfg.UnreadOnly {
		mode = "pending (unread) only"
	}
	fmt.Println("Mode:     " + mode)

	fmt.Println("\nReadiness:")
	arts, err := client.Unreads(ctx, cfg.UnreadOnly, 5)
	if err != nil {
		fmt.Printf("  [--] read articles: %s\n", err.Error())
		return nil
	}
	fmt.Printf("  [ok] read articles (%d fetched)\n", len(arts))
	if len(arts) > 0 {
		fmt.Printf("       newest: %q (%s)\n", arts[0].Title, arts[0].Feed)
	} else {
		fmt.Println("       nothing pending right now")
	}
	fmt.Println("  [ok] mark as read uses the same session (press r in the TUI)")
	return nil
}

// printCount prints the pending (unread) article count for the launcher's badge.
// It uses the same fetch cap as the TUI (MaxArticles) so the badge matches what
// you see inside; a capped result is reported as "N+".
func printCount(ctx context.Context, client *folo.Client, cfg config.Config) error {
	n, capped, err := folo.NewSource(client, true, cfg.MaxArticles).Count(ctx)
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

// printJSON dumps the pending (unread) articles as a JSON array for the
// launcher's "all" view. The list response carries only the short summary (the
// full body is a lazy per-entry fetch), so body is the summary here.
func printJSON(ctx context.Context, client *folo.Client, cfg config.Config) error {
	arts, err := client.Unreads(ctx, true, cfg.MaxArticles)
	if err != nil {
		return err
	}
	items := make([]dumpItem, 0, len(arts))
	for _, a := range arts {
		item := dumpItem{
			App:    "folo",
			ID:     a.ID,
			Title:  a.Title,
			Body:   a.Summary,
			Source: a.Feed,
			Author: a.Author,
			URL:    a.URL,
			Age:    a.Age,
		}
		if !a.Published.IsZero() {
			item.TS = a.Published.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return json.NewEncoder(os.Stdout).Encode(items)
}

// markReadFromStdin marks read every entry id on stdin (one per line) via the
// same session the standalone app uses, so posts triaged in the "all" view drop
// out of Folo's pending list everywhere.
func markReadFromStdin(ctx context.Context, client *folo.Client) error {
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
