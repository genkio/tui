// Command inoreader-tui is a terminal UI for triaging unread Inoreader
// articles. It reads the "All articles" stream oldest-first, expands items
// inline, opens them in the browser, and marks them read, talking directly to
// Inoreader's Reader API with your browser session cookie.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/genkio/inoreader-tui/internal/config"
	"github.com/genkio/inoreader-tui/internal/inoreader"
	"github.com/genkio/inoreader-tui/internal/ui"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inoreader-tui: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "verify the connection to Inoreader and exit")
		count       = flag.Bool("count", false, "print the unread article count and exit")
		configPath  = flag.String("config", "", "config file path (default: $XDG_CONFIG_HOME/inoreader-tui/config.toml)")
		refresh     = flag.Duration("refresh", 0, "auto-refresh the unread list at this interval (e.g. 5m); off if unset")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("inoreader-tui " + versionString())
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

	client := inoreader.New(cfg.BaseURL, config.Cookie(), cfg.UserAgent)

	if *check {
		return printCheck(ctx, client, cfg)
	}
	if *count {
		return printCount(ctx, client)
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

// countMax bounds how many unread items printCount pages through, so the
// launcher badge stays cheap; a full result is reported as "N+".
const countMax = 100

// printCount prints the number of unread articles for the launcher's badge.
func printCount(ctx context.Context, client *inoreader.Client) error {
	arts, err := client.Unreads(ctx, true, countMax)
	if err != nil {
		return err
	}
	suffix := ""
	if len(arts) >= countMax {
		suffix = "+"
	}
	fmt.Printf("%d%s\n", len(arts), suffix)
	return nil
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
