// Package bilibili runs the bilibili 动态 TUI: the video posts of the uploaders
// you follow, inline expand, browser open, read from the same web-dynamic JSON
// API t.bilibili.com calls, with your browser session. The tui launcher
// dispatches `tui bilibili` to Main; the standalone bilibili-tui binary wraps it
// too.
package bilibili

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
	"github.com/genkio/tui/plugins/bilibili/internal/bilibili"
	"github.com/genkio/tui/plugins/bilibili/internal/config"
	"github.com/genkio/tui/plugins/bilibili/internal/readstore"
	"github.com/genkio/tui/plugins/bilibili/internal/ui"
)

// version is overridden at build time via -ldflags "-X ...plugins/bilibili.version=...".
var version = "dev"

func Main() int {
	core.LoadUserEnv()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bilibili-tui: "+err.Error())
		return 1
	}
	return 0
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "fetch one page of the 动态 timeline and exit")
		count       = flag.Bool("count", false, "print the unread video-post count and exit")
		dumpJSON    = flag.Bool("json", false, "print unread video posts as JSON and exit (for the 'all' timeline)")
		maxItems    = flag.Int("max", 0, "with --json: fetch up to this many, past the config cap (the web server's backlog sweep asks for more than a screenful)")
		markRead    = flag.Bool("mark-read", false, "mark read the dynamic ids read from stdin (one per line) and exit")
		auth        = flag.Bool("auth", false, "log in via a browser and capture the session into ~/.config/tui/env")
		configPath  = flag.String("config", "", "config file path (default: $XDG_CONFIG_HOME/bilibili-tui/config.toml)")
		refresh     = flag.Duration("refresh", 0, "auto-refresh the timeline at this interval (e.g. 2m); off if unset")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("bilibili-tui " + versionString())
		return nil
	}
	if *auth {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return core.RunAuth(ctx, "https://t.bilibili.com", func(s *core.Session) (map[string]string, error) {
			hdr := s.CookieHeader("bilibili.com")
			// SESSDATA is bilibili's login cookie; without it the 动态 feed
			// answers 账号未登录.
			if hdr == "" || !strings.Contains(hdr, "SESSDATA") {
				return nil, errors.New("could not read a bilibili session (SESSDATA cookie); were you fully logged in?")
			}
			return map[string]string{"BLTUI_COOKIE": hdr}, nil
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

	client := bilibili.New(config.Cookie(), config.UserAgent())

	if *check {
		return printCheck(ctx, client, cfg)
	}
	if *count {
		return printCount(ctx, client, cfg)
	}
	if *dumpJSON {
		if *maxItems > 0 {
			cfg.MaxVideos = *maxItems
		}
		return printJSON(ctx, client, cfg)
	}

	interval := cfg.RefreshInterval()
	if *refresh > 0 { // an explicit flag wins over the config/env value
		interval = *refresh
	}
	return ui.Run(ctx, client, cfg, interval)
}

// printCheck verifies the session works against the 动态 timeline.
func printCheck(ctx context.Context, client *bilibili.Client, cfg config.Config) error {
	fmt.Println("bilibili-tui " + versionString())
	fmt.Println("\nReadiness:")
	videos, err := client.Feed(ctx, 5)
	if err != nil {
		fmt.Printf("  [--] 动态 timeline   %s\n", err.Error())
		return nil
	}
	fmt.Printf("  [ok] 动态 timeline   %d video posts\n", len(videos))
	if len(videos) > 0 {
		fmt.Printf("       top: %s — %s\n", videos[0].Author, firstLine(videos[0].Title, 60))
		fmt.Printf("       url: %s\n", videos[0].URL)
	}
	return nil
}

// printCount prints how many posts in the fetched window are still unread (not
// in the local read store), for the launcher's badge. When every fetched post is
// unread there are almost certainly more beyond the window, so it's reported as
// "N+"; the launcher treats that as saturated and stops polling.
func printCount(ctx context.Context, client *bilibili.Client, cfg config.Config) error {
	src := bilibili.NewSource(client, readstore.Load(""), cfg.MaxVideos)
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

// printJSON dumps the unread video posts as a JSON array for the launcher's
// "all" view, applying the same local read filter as --count so the two stay
// consistent. core.Wire is the shape every app emits, so a field added there
// reaches the merged view without touching this.
func printJSON(ctx context.Context, client *bilibili.Client, cfg config.Config) error {
	src := bilibili.NewSource(client, readstore.Load(""), cfg.MaxVideos)
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

// markReadFromStdin marks read every dynamic id on stdin (one per line), so the
// launcher's "all" view can flush the posts you triaged there into the same local
// store the standalone app reads.
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
