// Package reddit runs the Reddit home-timeline TUI: the authenticated user's
// front page (r/…), inline expand, browser open, reading old.reddit.com's
// JSON API with your browser session. The tui launcher dispatches `tui reddit`
// to Main; the standalone reddit-tui binary wraps it too.
package reddit

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
	"github.com/genkio/tui/plugins/reddit/internal/config"
	"github.com/genkio/tui/plugins/reddit/internal/readstore"
	"github.com/genkio/tui/plugins/reddit/internal/reddit"
	"github.com/genkio/tui/plugins/reddit/internal/ui"
)

// version is overridden at build time via -ldflags "-X ...plugins/reddit.version=...".
var version = "dev"

func Main() int {
	core.LoadUserEnv()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "reddit-tui: "+err.Error())
		return 1
	}
	return 0
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "fetch one page of the home timeline and exit")
		count       = flag.Bool("count", false, "print the unread post count and exit")
		dumpJSON    = flag.Bool("json", false, "print unread posts as JSON and exit (for the 'all' timeline)")
		maxItems    = flag.Int("max", 0, "with --json: fetch up to this many, past the config cap (the web server's backlog sweep asks for more than a screenful)")
		markRead    = flag.Bool("mark-read", false, "mark read the post ids read from stdin (one per line) and exit")
		auth        = flag.Bool("auth", false, "log in via a browser and capture the session into ~/.config/tui/env")
		configPath  = flag.String("config", "", "config file path (default: $XDG_CONFIG_HOME/reddit-tui/config.toml)")
		refresh     = flag.Duration("refresh", 0, "auto-refresh the timeline at this interval (e.g. 2m); off if unset")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("reddit-tui " + versionString())
		return nil
	}
	if *auth {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return core.RunAuth(ctx, "https://www.reddit.com", func(s *core.Session) (map[string]string, error) {
			hdr := s.CookieHeader("reddit.com")
			if hdr == "" || !strings.Contains(hdr, "reddit_session") {
				return nil, errors.New("could not read a reddit session (reddit_session cookie); were you fully logged in?")
			}
			return map[string]string{"RDTUI_COOKIE": hdr}, nil
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

	client := reddit.New(config.Cookie())

	if *check {
		return printCheck(ctx, client)
	}
	if *count {
		return printCount(ctx, client, cfg)
	}
	if *dumpJSON {
		if *maxItems > 0 {
			cfg.MaxPosts = *maxItems
		}
		return printJSON(ctx, client, cfg)
	}

	interval := cfg.RefreshInterval()
	if *refresh > 0 { // an explicit flag wins over the config/env value
		interval = *refresh
	}
	return ui.Run(ctx, client, cfg, interval)
}

// printCheck verifies the session works against the home timeline.
func printCheck(ctx context.Context, client *reddit.Client) error {
	fmt.Println("reddit-tui " + versionString())
	fmt.Println("\nReadiness:")
	posts, err := client.Home(ctx, 5)
	if err != nil {
		fmt.Printf("  [--] home timeline   %s\n", err.Error())
		return nil
	}
	fmt.Printf("  [ok] home timeline   %d posts\n", len(posts))
	if len(posts) > 0 {
		fmt.Printf("       top: r/%s — %s\n", posts[0].Subreddit, firstLine(posts[0].Title, 60))
	}
	return nil
}

// printCount prints how many posts in the newest page are still unread (not in
// the local read store), for the launcher's badge. When every fetched post is
// unread there are almost certainly more beyond the page, so it's reported as
// "N+"; the launcher treats that as saturated and stops polling. A read post in
// the window marks where you left off, so a partial count is treated as complete.
func printCount(ctx context.Context, client *reddit.Client, cfg config.Config) error {
	src := reddit.NewSource(client, readstore.Load(""), cfg.MaxPosts)
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

// printJSON dumps the unread posts of the home timeline as a JSON array for the
// launcher's "all" view, applying the same local read filter as --count so the
// two stay consistent. core.Wire is the shape every app emits, so a field added
// there (images, video) reaches the merged view without touching this.
func printJSON(ctx context.Context, client *reddit.Client, cfg config.Config) error {
	src := reddit.NewSource(client, readstore.Load(""), cfg.MaxPosts)
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
