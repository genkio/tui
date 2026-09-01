package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genkio/tui/core"
)

const localFeedServer = "http://127.0.0.1:8080"

func runCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return runServeCommand(args[1:])
		case "web":
			return runWebCommand(args[1:])
		case "all":
			return runAllCommand(args[1:])
		}
	}
	return runAllCommand(args)
}

func runServeCommand(args []string) error {
	flags := flag.NewFlagSet("tui serve", flag.ContinueOnError)
	addr := flags.String("addr", "0.0.0.0:8080", "address for the API and web UI")
	every := flags.Duration("fetch-every", 15*time.Minute, "feed fetch interval; 0 disables scheduled fetches")
	drain := flags.Bool("drain-inoreader", true, "page through Inoreader by marking fetched entries upstream")
	dev := flags.Bool("dev", false, "reload cmd/tui/page.tmpl on every web request")
	syncDir := flags.String("sync-dir", os.Getenv("TUI_SYNC_DIR"), "directory for snapshots, credentials, read state, and configs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("tui serve: unexpected argument %q", flags.Arg(0))
	}
	if *syncDir != "" {
		exportSyncDir(*syncDir)
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	return runServer(root, *addr, *dev, *drain, *every)
}

func runWebCommand(args []string) error {
	flags := flag.NewFlagSet("tui web", flag.ContinueOnError)
	server := flags.String("server", defaultFeedServer(), "feed server URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("tui web: unexpected argument %q", flags.Arg(0))
	}
	normalized, err := normalizeServerURL(*server)
	if err != nil {
		return err
	}
	return core.OpenInBrowser(normalized + "/")
}

func runAllCommand(args []string) error {
	flags := flag.NewFlagSet("tui all", flag.ContinueOnError)
	server := flags.String("server", defaultFeedServer(), "feed server URL")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("tui all: unexpected argument %q", flags.Arg(0))
	}
	if *showVersion {
		fmt.Println("tui " + version)
		return nil
	}
	normalized, err := normalizeServerURL(*server)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(newAllClientModel(normalized)).Run()
	return err
}

func defaultFeedServer() string {
	if server := strings.TrimSpace(os.Getenv("TUI_SERVER_URL")); server != "" {
		return server
	}
	return localFeedServer
}

func normalizeServerURL(server string) (string, error) {
	u, err := serverEndpoint(strings.TrimSpace(server), "/")
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}
