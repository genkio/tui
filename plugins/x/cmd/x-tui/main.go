// Command x-tui is the standalone build of the x.com timeline TUI. The launcher
// runs the same code in-process via `tui x`; this wrapper keeps the app
// buildable and runnable on its own.
package main

import (
	"os"

	"github.com/genkio/tui/plugins/x"
)

func main() { os.Exit(x.Main()) }
