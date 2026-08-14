package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/bilibili"
	"github.com/genkio/tui/plugins/douban"
	"github.com/genkio/tui/plugins/folo"
	"github.com/genkio/tui/plugins/inoreader"
	"github.com/genkio/tui/plugins/reddit"
	"github.com/genkio/tui/plugins/slack"
	"github.com/genkio/tui/plugins/x"
)

// pluginMains maps a subcommand name to that plugin's entrypoint. Because every
// app is compiled into this one binary, `tui x` runs the x app directly, with
// no Go toolchain, make, or source tree at runtime. The launcher re-execs
// itself the same way to open an app or read its counts.
var pluginMains = map[string]func() int{
	"x":         x.Main,
	"inoreader": inoreader.Main,
	"slack":     slack.Main,
	"folo":      folo.Main,
	"reddit":    reddit.Main,
	"douban":    douban.Main,
	"bilibili":  bilibili.Main,
}

// runPluginIfRequested runs a plugin and exits when the first argument names
// one, so this binary doubles as every app. The subcommand is dropped from
// os.Args first so the plugin's own flag parser sees only its flags.
func runPluginIfRequested() {
	if len(os.Args) < 2 {
		return
	}
	run, ok := pluginMains[os.Args[1]]
	if !ok {
		return
	}
	os.Args = takeStateDir(os.Args)
	os.Args = append(os.Args[:1], os.Args[2:]...)
	os.Exit(run())
}

// takeStateDir turns the launcher's --state-dir into the env var the state
// paths actually read, and takes it out of the arguments. A plugin runs from
// here, before the launcher parses anything, so its own flag set is the only
// one in play — and it does not define this flag, which is why `tui x
// --state-dir DIR` used to die on "flag provided but not defined". Both flag
// spellings and both value forms are accepted, the same as Go's flag package.
func takeStateDir(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(args[i], "=")
		if name != "-state-dir" && name != "--state-dir" {
			out = append(out, args[i])
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "tui: -state-dir needs a directory")
				os.Exit(2)
			}
			i++
			val = args[i]
		}
		exportStateDir(val)
	}
	return out
}

// exportStateDir publishes the state dir for everything that reads it: this
// process's own core.StatePath/ConfigPath calls and any `tui <app>` subprocess
// it spawns. Stored normalized (~ and relative paths expanded) so a child with
// a different working directory resolves the same files.
func exportStateDir(dir string) {
	os.Setenv("TUI_STATE_DIR", dir)
	os.Setenv("TUI_STATE_DIR", core.StateDir())
}

// self is the path to this binary, for re-running a plugin as `tui <app>`.
func self() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return os.Args[0]
}

// appEnv is the current environment with the app's per-plugin .env applied on
// top, for the dev/source-tree layout. A Homebrew install has no per-plugin
// .env; there creds come from core.LoadUserEnv and are already in os.Environ().
func appEnv(dir string) []string {
	env := map[string]string{}
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		}
	}
	for k, v := range core.ParseEnvFile(filepath.Join(dir, ".env")) {
		env[k] = v
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
