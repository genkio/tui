package main

import (
	"os"
	"path/filepath"
	"strings"

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

// appEnv returns the current environment with the app's .env applied on top,
// the way `make run` sourced it, so a self-exec'd plugin still finds its session
// vars. This bridges the pre-Homebrew layout where creds live in a per-plugin
// .env; once creds move to the user config dir it can go.
func appEnv(dir string) []string {
	vars := map[string]string{}
	var order []string
	set := func(k, v string) {
		if _, seen := vars[k]; !seen {
			order = append(order, k)
		}
		vars[k] = v
	}
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			set(k, v)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		return os.Environ()
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k != "" {
			set(k, unquoteEnv(strings.TrimSpace(v)))
		}
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+vars[k])
	}
	return out
}

// unquoteEnv undoes the shell single-quoting upsert-env.mjs writes
// (export KEY='value', with an embedded ' escaped as '\''), and plain double
// quotes, leaving bare values untouched.
func unquoteEnv(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], `'\''`, `'`)
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}
