// Package douban runs the Douban following-timeline TUI: the 友邻广播 stream
// of the people you follow (statuses, reshares, 看过/在读 marks), inline
// expand, browser open, reading m.douban.com's rexxar JSON API with your
// browser session. The tui launcher dispatches `tui douban` to Main; the
// standalone douban-tui binary wraps it too.
package douban

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
	"time"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/douban/internal/config"
	"github.com/genkio/tui/plugins/douban/internal/douban"
	"github.com/genkio/tui/plugins/douban/internal/readstore"
	"github.com/genkio/tui/plugins/douban/internal/ui"
)

// version is overridden at build time via -ldflags "-X ...plugins/douban.version=...".
var version = "dev"

func Main() int {
	core.LoadUserEnv()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "douban-tui: "+err.Error())
		return 1
	}
	return 0
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "fetch one page of the following timeline and exit")
		count       = flag.Bool("count", false, "print the unread status count and exit")
		dumpJSON    = flag.Bool("json", false, "print unread statuses as JSON and exit (for the 'all' timeline)")
		maxItems    = flag.Int("max", 0, "with --json: fetch up to this many, past the config cap (the web server's backlog sweep asks for more than a screenful)")
		markRead    = flag.Bool("mark-read", false, "mark read the status ids read from stdin (one per line) and exit")
		auth        = flag.Bool("auth", false, "log in via a browser and capture the session into ~/.config/tui/env")
		configPath  = flag.String("config", "", "config file path (default: $XDG_CONFIG_HOME/douban-tui/config.toml)")
		refresh     = flag.Duration("refresh", 0, "auto-refresh the timeline at this interval (e.g. 2m); off if unset")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("douban-tui " + versionString())
		return nil
	}
	if *auth {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return core.RunAuth(ctx, "https://www.douban.com", func(s *core.Session) (map[string]string, error) {
			hdr := s.CookieHeader("douban.com")
			// dbcl2 is douban's login cookie; without it the timeline is a wall.
			if hdr == "" || !strings.Contains(hdr, "dbcl2") {
				return nil, errors.New("could not read a douban session (dbcl2 cookie); were you fully logged in?")
			}
			return map[string]string{"DBTUI_COOKIE": hdr}, nil
		})
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	// Marking read only touches the local read store, so it needs neither a
	// session nor the network; handle it before the auth check.
	if *markRead {
		return markReadFromStdin()
	}

	if err := config.ValidateAuth(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := douban.New(config.Cookie(), config.UserAgent())

	if *check {
		return printCheck(ctx, client, cfg)
	}
	if *count {
		return printCount(ctx, client, cfg)
	}
	if *dumpJSON {
		if *maxItems > 0 {
			cfg.MaxStatuses = *maxItems
		}
		return printJSON(ctx, client, cfg)
	}

	interval := cfg.RefreshInterval()
	if *refresh > 0 { // an explicit flag wins over the config/env value
		interval = *refresh
	}
	return ui.Run(ctx, client, cfg, interval)
}

// printCheck verifies the session works against the following timeline, and
// that the configured charts still answer.
func printCheck(ctx context.Context, client *douban.Client, cfg config.Config) error {
	fmt.Println("douban-tui " + versionString())
	fmt.Println("\nReadiness:")
	statuses, err := client.Home(ctx, 5)
	if err != nil {
		fmt.Printf("  [--] following timeline   %s\n", err.Error())
		return nil
	}
	fmt.Printf("  [ok] following timeline   %d statuses\n", len(statuses))
	if len(statuses) > 0 {
		fmt.Printf("       top: %s — %s\n", statuses[0].Author, firstLine(statuses[0].Text, 60))
	}
	for _, id := range cfg.Charts {
		entries := client.Charts(ctx, []string{id}, time.Now())
		if len(entries) == 0 {
			fmt.Printf("  [--] chart %-22s no entries\n", id)
			continue
		}
		fmt.Printf("  [ok] chart %-22s %d entries — %s\n", id, len(entries), entries[0].Title)
	}
	return nil
}

// printCount prints how many statuses in the newest page are still unread (not
// in the local read store), for the launcher's badge. When every fetched
// status is unread there are almost certainly more beyond the page, so it's
// reported as "N+"; the launcher treats that as saturated and stops polling. A
// read status in the window marks where you left off, so a partial count is
// treated as complete.
func printCount(ctx context.Context, client *douban.Client, cfg config.Config) error {
	src := douban.NewSource(client, readstore.Load(""), cfg.MaxStatuses, cfg.Charts)
	n, capped, err := src.Count(ctx)
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

// printJSON dumps the unread statuses of the following timeline as a JSON
// array for the launcher's "all" view, applying the same local read filter as
// --count so the two stay consistent. core.Wire is the shape every app emits,
// so a field added there (images, video) reaches the merged view without
// touching this.
func printJSON(ctx context.Context, client *douban.Client, cfg config.Config) error {
	src := douban.NewSource(client, readstore.Load(""), cfg.MaxStatuses, cfg.Charts)
	items, err := src.Fetch(ctx)
	if err != nil {
		return err
	}
	out := make([]core.Wire, 0, len(items))
	for _, it := range items {
		out = append(out, it.Wire())
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

// markReadFromStdin marks read every status id on stdin (one per line), so the
// launcher's "all" view can flush the statuses you triaged there into the same
// local store the standalone app reads.
func markReadFromStdin() error {
	read := readstore.Load("")
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if id := strings.TrimSpace(sc.Text()); id != "" {
			read.Mark(id)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return read.Save()
}

func firstLine(s string, max int) string {
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	if len([]rune(s)) > max {
		return string([]rune(s)[:max]) + "..."
	}
	return s
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
