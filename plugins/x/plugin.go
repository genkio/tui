// Package x runs the x.com home-timeline TUI: the For You and Following feeds
// (tab to switch), inline expand, browser open, talking to x.com's web GraphQL
// API with your browser session. The tui launcher dispatches `tui x` to Main;
// the standalone x-tui binary wraps it too.
package x

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
	"github.com/genkio/tui/plugins/x/internal/config"
	"github.com/genkio/tui/plugins/x/internal/readstore"
	"github.com/genkio/tui/plugins/x/internal/ui"
	"github.com/genkio/tui/plugins/x/internal/x"
)

// version is overridden at build time via -ldflags "-X ...plugins/x.version=...".
var version = "dev"

func Main() int {
	core.LoadUserEnv()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "x-tui: "+err.Error())
		return 1
	}
	return 0
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "fetch one page from each timeline and exit")
		count       = flag.Bool("count", false, "print the unread post count and exit")
		dumpJSON    = flag.Bool("json", false, "print unread posts as JSON and exit (for the 'all' timeline)")
		markRead    = flag.Bool("mark-read", false, "mark read the post ids read from stdin (one per line) and exit")
		auth        = flag.Bool("auth", false, "log in via a browser and capture the session into ~/.config/tui/env")
		configPath  = flag.String("config", "", "config file path (default: $XDG_CONFIG_HOME/x-tui/config.toml)")
		refresh     = flag.Duration("refresh", 0, "auto-refresh the timeline at this interval (e.g. 2m); off if unset")
		tabFlag     = flag.String("tab", "", "x timeline to read: foryou | following (default from config)")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("x-tui " + versionString())
		return nil
	}
	if *auth {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return core.RunAuth(ctx, "https://x.com/home", func(s *core.Session) (map[string]string, error) {
			token := s.Cookie("auth_token", "x.com", "twitter.com")
			ct0 := s.Cookie("ct0", "x.com", "twitter.com")
			if token == "" || ct0 == "" {
				return nil, errors.New("could not read auth_token/ct0 cookies; were you fully logged in?")
			}
			return map[string]string{"XTUI_AUTH_TOKEN": token, "XTUI_CT0": ct0}, nil
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

	client := x.New(config.AuthToken(), config.CSRF(), cfg.Bearer, cfg.Lang)

	// resolveTab lets the 'all' view (which drives --json/--count) switch x
	// between For You and Following per request, falling back to the config default.
	tab := resolveTab(cfg, *tabFlag)

	if *check {
		return printCheck(ctx, client)
	}
	if *count {
		return printCount(ctx, client, cfg, tab)
	}
	if *dumpJSON {
		return printJSON(ctx, client, cfg, tab)
	}

	interval := cfg.RefreshInterval()
	if *refresh > 0 { // an explicit flag wins over the config/env value
		interval = *refresh
	}
	return ui.Run(ctx, client, cfg, interval)
}

// printCheck probes both timelines and reports whether reading will work.
func printCheck(ctx context.Context, client *x.Client) error {
	fmt.Println("x-tui " + versionString())
	fmt.Println("\nReadiness:")
	for _, tab := range []x.Tab{x.ForYou, x.Following} {
		tweets, err := client.Timeline(ctx, tab, 5)
		if err != nil {
			fmt.Printf("  [--] %-10s %s\n", tab.String(), err.Error())
			continue
		}
		fmt.Printf("  [ok] %-10s %d posts\n", tab.String(), len(tweets))
		if len(tweets) > 0 {
			fmt.Printf("       top: @%s: %s\n", tweets[0].Handle, firstLine(tweets[0].Text, 60))
		}
	}
	return nil
}

// printCount prints how many posts in the default timeline's newest page are
// still unread (not in the local read store), for the launcher's badge. When
// every fetched post is unread there are almost certainly more beyond the page,
// so it's reported as "N+"; the launcher treats that as saturated and stops
// polling. A read post in the window marks where you left off, so a partial
// count is treated as complete.

// resolveTab picks the x timeline: the --tab flag wins, else the config default.
func resolveTab(cfg config.Config, flagTab string) x.Tab {
	for _, s := range []string{flagTab, cfg.DefaultTab} {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "foryou", "for you", "for-you":
			return x.ForYou
		}
	}
	return x.Following
}

func printCount(ctx context.Context, client *x.Client, cfg config.Config, tab x.Tab) error {
	src := x.NewSource(client, readstore.Load(""), tab, cfg.MaxTweets)
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

// printJSON dumps the unread posts of the default timeline as a JSON array for
// the launcher's "all" view, applying the same local read filter as --count so
// the two stay consistent. It emits the same normalization the in-process feed
// uses, so the merged view and the standalone app can't drift apart.
func printJSON(ctx context.Context, client *x.Client, cfg config.Config, tab x.Tab) error {
	tweets, err := client.Timeline(ctx, tab, cfg.MaxTweets)
	if err != nil {
		return err
	}
	read := readstore.Load("")
	items := make([]core.Wire, 0, len(tweets))
	for _, t := range tweets {
		if read.Has(t.ID) {
			continue
		}
		items = append(items, x.ToItem(t).Wire())
	}
	return json.NewEncoder(os.Stdout).Encode(items)
}

// markReadFromStdin marks read every post id on stdin (one per line), so the
// launcher's "all" view can flush the posts you triaged there into the same
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
	if len(s) > max {
		return s[:max] + "..."
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
