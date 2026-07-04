// Command slack-tui is the standalone build of the Slack triage TUI. The
// launcher runs the same code in-process via `tui slack`; this wrapper keeps
// the app buildable and runnable on its own.
package main

import (
	"os"

	"github.com/genkio/tui/plugins/slack"
)

func main() { os.Exit(slack.Main()) }
