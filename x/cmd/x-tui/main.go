// Command x-tui is a terminal UI for reading your x.com home timelines. It lists
// the For You and Following feeds (tab to switch), expands posts inline, and
// opens them in the browser, talking directly to x.com's web GraphQL API with
// your browser session.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"

	"github.com/genkio/x-tui/internal/config"
	"github.com/genkio/x-tui/internal/readstore"
	"github.com/genkio/x-tui/internal/ui"
	"github.com/genkio/x-tui/internal/x"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "x-tui: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "fetch one page from each timeline and exit")
		count       = flag.Bool("count", false, "print the unread post count and exit")
		configPath  = flag.String("config", "", "config file path (default: $XDG_CONFIG_HOME/x-tui/config.toml)")
		refresh     = flag.Duration("refresh", 0, "auto-refresh the timeline at this interval (e.g. 2m); off if unset")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("x-tui " + versionString())
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

	client := x.New(config.AuthToken(), config.CSRF(), cfg.Bearer, cfg.Lang)

	if *check {
		return printCheck(ctx, client)
	}
	if *count {
		return printCount(ctx, client, cfg)
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
func printCount(ctx context.Context, client *x.Client, cfg config.Config) error {
	tab := x.Following
	switch strings.ToLower(strings.TrimSpace(cfg.DefaultTab)) {
	case "foryou", "for you", "for-you":
		tab = x.ForYou
	}
	tweets, err := client.Timeline(ctx, tab, cfg.MaxTweets)
	if err != nil {
		return err
	}
	read := readstore.Load("")
	unread := 0
	for _, t := range tweets {
		if !read.Has(t.ID) {
			unread++
		}
	}
	suffix := ""
	if len(tweets) > 0 && unread >= len(tweets) {
		suffix = "+"
	}
	fmt.Printf("%d%s\n", unread, suffix)
	return nil
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
