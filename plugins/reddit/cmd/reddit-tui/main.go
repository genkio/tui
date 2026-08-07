// Command reddit-tui is the standalone build of the Reddit home-timeline TUI.
// The launcher runs the same code in-process via `tui reddit`; this wrapper
// keeps the app buildable and runnable on its own.
package main

import (
	"os"

	"github.com/genkio/tui/plugins/reddit"
)

func main() { os.Exit(reddit.Main()) }
