// Command folo-tui is a terminal UI for triaging your pending (unread) Folo
// articles. It reads the Articles timeline, expands items inline, opens them in
// the browser, and marks them read, talking directly to Folo's web API with
// your browser session cookie.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/genkio/folo-tui/internal/config"
	"github.com/genkio/folo-tui/internal/folo"
	"github.com/genkio/folo-tui/internal/ui"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "folo-tui: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "verify the connection to Folo and exit")
		count       = flag.Bool("count", false, "print the pending (unread) article count and exit")
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
	arts, err := client.Unreads(ctx, true, cfg.MaxArticles)
	if err != nil {
		return err
	}
	suffix := ""
	if len(arts) >= cfg.MaxArticles {
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
