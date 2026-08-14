// Command bilibili-tui is the standalone build of the bilibili 动态 TUI. The
// launcher runs the same code in-process via `tui bilibili`; this wrapper keeps
// the app buildable and runnable on its own.
package main

import (
	"os"

	"github.com/genkio/tui/plugins/bilibili"
)

func main() { os.Exit(bilibili.Main()) }
