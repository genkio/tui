// Command inoreader-tui is the standalone build of the Inoreader triage TUI.
// The launcher runs the same code in-process via `tui inoreader`; this wrapper
// keeps the app buildable and runnable on its own.
package main

import (
	"os"

	"github.com/genkio/tui/plugins/inoreader"
)

func main() { os.Exit(inoreader.Main()) }
