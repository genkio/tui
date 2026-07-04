// Command folo-tui is the standalone build of the Folo triage TUI. The launcher
// runs the same code in-process via `tui folo`; this wrapper keeps the app
// buildable and runnable on its own.
package main

import (
	"os"

	"github.com/genkio/tui/plugins/folo"
)

func main() { os.Exit(folo.Main()) }
