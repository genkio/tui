package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/genkio/tui/core"
	"github.com/genkio/tui/plugins/folo"
	"github.com/genkio/tui/plugins/inoreader"
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
	os.Args = append(os.Args[:1], os.Args[2:]...)
	os.Exit(run())
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
