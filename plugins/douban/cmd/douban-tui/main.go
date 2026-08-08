// Command douban-tui is the standalone build of the Douban following-timeline
// TUI. The launcher runs the same code in-process via `tui douban`; this
// wrapper keeps the app buildable and runnable on its own.
package main

import (
	"os"

	"github.com/genkio/tui/plugins/douban"
)

func main() { os.Exit(douban.Main()) }
