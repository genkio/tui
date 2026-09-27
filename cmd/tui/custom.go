package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/core/custom"
)

// A custom plugin is a parser file in core.PluginsDir() rather than a plugin
// compiled into this binary: no login, no subprocess, no read state of its own
// upstream. The file's name is the source's name (forum.js is "forum"), and
// it is swept, cached, counted and marked like any other source from there on.
//
// The directory is read again on every page load and sweep, and the file on
// every fetch, so dropping a parser in or editing one needs no restart.

const customColor = "#8a8f98"

var customNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type customPlugin struct {
	name string
	path string
	meta custom.Meta
	mod  time.Time
	size int64
}

type customRegistry struct {
	mu      sync.Mutex
	plugins map[string]customPlugin
	warned  map[string]bool // files already reported as unusable, so a page load doesn't repeat it
}

var customs = &customRegistry{plugins: map[string]customPlugin{}, warned: map[string]bool{}}

// scan re-reads the plugins directory and returns the custom sources in it,
// sorted. A file is evaluated again only when it has changed. One that fails
// to load is still a source: its fetch fails the same way, and the chip going
// red with the error is how a broken parser gets noticed.
func (c *customRegistry) scan() []string {
	dir := core.PluginsDir()
	var entries []os.DirEntry
	if dir != "" {
		entries, _ = os.ReadDir(dir) // no directory is no custom plugins
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	next := map[string]customPlugin{}
	for _, e := range entries {
		file := e.Name()
		if e.IsDir() || !strings.HasSuffix(file, ".js") {
			continue
		}
		name := strings.TrimSuffix(file, ".js")
		if reason := customNameProblem(name); reason != "" {
			if !c.warned[file] {
				c.warned[file] = true
				logf("custom plugin %s skipped: %s", file, reason)
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if p, ok := c.plugins[name]; ok && p.mod.Equal(info.ModTime()) && p.size == info.Size() {
			next[name] = p
			continue
		}
		p := customPlugin{name: name, path: filepath.Join(dir, file), mod: info.ModTime(), size: info.Size()}
		if p.meta, err = custom.Load(p.path); err != nil {
			logf("custom plugin %s: %v", name, err)
		} else {
			logf("custom plugin %s loaded from %s", name, p.path)
		}
		next[name] = p
	}
	c.plugins = next
	names := make([]string, 0, len(next))
	for name := range next {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *customRegistry) lookup(name string) (customPlugin, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.plugins[name]
	return p, ok
}

// customNameProblem says why a file name cannot be a source, or "" when it
// can. The name goes into URLs and feed keys, and taking a built-in's would
// file one service's posts under another.
func customNameProblem(name string) string {
	if !customNameRe.MatchString(name) {
		return "name must be lowercase letters, digits, - and _"
	}
	if _, ok := pluginMains[name]; ok || name == xForYouApp || name == allApp {
		return "name is taken by a built-in source"
	}
	return ""
}

// fetchCustom runs a custom plugin's parser. max is applied here, newest kept:
// a parser hands over whatever its page holds, and a whole thread is a
// thousand posts.
func fetchCustom(ctx context.Context, p customPlugin, max int, now time.Time) ([]core.Item, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, sweepAppTimeout)
	defer cancel()
	ws, err := custom.Fetch(ctx, p.path)
	if err != nil {
		return nil, false, fmt.Errorf("%s", firstLine(err.Error()))
	}
	items := make([]core.Item, 0, len(ws))
	for _, w := range ws {
		w.App = p.name
		items = append(items, w.Item(now))
	}
	if max > 0 && len(items) > max {
		core.MergeSort(items)
		items = items[:max]
	}
	return items, false, nil
}

// runCustomCommand runs one parser and prints what it found, the way `tui
// <app> --json` does, for writing one without waiting on a sweep. It takes a
// path, or a name to look up in the plugins directory.
func runCustomCommand(args []string) error {
	flags := flag.NewFlagSet("tui custom", flag.ContinueOnError)
	syncDir := flags.String("sync-dir", os.Getenv("TUI_SYNC_DIR"), "directory whose plugins/ holds the parser, when it is named rather than given as a path")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: tui custom [--sync-dir DIR] <name | file.js>")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return flag.ErrHelp
	}
	if *syncDir != "" {
		exportSyncDir(*syncDir)
	}
	path := flags.Arg(0)
	if !strings.HasSuffix(path, ".js") {
		path = filepath.Join(core.PluginsDir(), path+".js")
	}
	ctx, cancel := context.WithTimeout(context.Background(), sweepAppTimeout)
	defer cancel()
	ws, err := custom.Fetch(ctx, path)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(path), ".js")
	for i := range ws {
		ws[i].App = name
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(ws)
}
