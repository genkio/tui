// Package slack runs the Slack unread-triage TUI. It spawns slack-mcp-server as
// a child process and talks to it over MCP; it never calls the Slack API or
// handles tokens itself. The tui launcher dispatches `tui slack` to Main; the
// standalone binary wraps it too.
package slack

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/slack/internal/config"
	"github.com/genkio/tui/plugins/slack/internal/mcp"
	"github.com/genkio/tui/plugins/slack/internal/ui"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func Main() int {
	core.LoadUserEnv()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "slack-tui: "+err.Error())
		return 1
	}
	return 0
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		check       = flag.Bool("check", false, "connect to the server, list its tools, and exit")
		count       = flag.Bool("count", false, "print the unread message count and exit")
		configPath  = flag.String("config", "", "config file path (default: $XDG_CONFIG_HOME/slack-tui/config.toml)")
		refresh     = flag.Duration("refresh", 0, "auto-refresh the unread list at this interval (e.g. 30s, 2m); off if unset")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("slack-tui " + versionString())
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := config.ValidateAuth(); err != nil {
		return err
	}

	// Cancel the spawn/handshake cleanly if the user hits Ctrl-C while waiting.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := mcp.Connect(ctx, cfg.Server)
	if err != nil {
		return err
	}
	defer client.Close()

	if *check {
		printCheck(client)
		return nil
	}
	if *count {
		return printCount(ctx, mcp.NewSource(client, cfg.Unreads))
	}

	interval := cfg.RefreshInterval()
	if *refresh > 0 { // an explicit flag wins over the config/env value
		interval = *refresh
	}
	return ui.Run(ctx, client, cfg, interval)
}

// printCheck reports the tools the server advertises and whether the features
// the TUI relies on are usable. This doubles as contract verification.
func printCheck(client *mcp.Client) {
	fmt.Println("Connected to slack-mcp-server.")

	fmt.Println("\nAdvertised tools:")
	for _, name := range client.ToolNames() {
		fmt.Println("  - " + name)
	}

	fmt.Println("\nReadiness:")
	for _, r := range []struct{ tool, label string }{
		{mcp.ToolUnreads, "list unreads"},
		{mcp.ToolHistory, "read messages"},
		{mcp.ToolReplies, "read threads"},
	} {
		fmt.Printf("  %s  %s (%s)\n", yesNo(client.HasTool(r.tool)), r.label, r.tool)
	}

	markReady := client.HasTool(mcp.ToolMark) && config.MarkToolEnabled()
	fmt.Printf("  %s  mark as read (%s)\n", yesNo(markReady), mcp.ToolMark)
	if client.HasTool(mcp.ToolMark) && !config.MarkToolEnabled() {
		fmt.Println("       set SLACK_MCP_MARK_TOOL=true to enable marking")
	}

	reactReady := client.HasTool(mcp.ToolReactionAdd) && config.ReactionToolEnabled()
	fmt.Printf("  %s  add reactions (%s)\n", yesNo(reactReady), mcp.ToolReactionAdd)
	if client.HasTool(mcp.ToolReactionAdd) && !config.ReactionToolEnabled() {
		fmt.Println("       set SLACK_MCP_REACTION_TOOL=true to enable reactions")
	}
}

func yesNo(ok bool) string {
	if ok {
		return "[ok]"
	}
	return "[--]"
}

// printCount prints the total unread messages across unread conversations for
// the launcher's badge. The conversation list is bounded by max_channels, so a
// full list is reported as "N+".
func printCount(ctx context.Context, src *mcp.Source) error {
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
