package core

import (
	"os"
	"path/filepath"
	"strings"
)

// UserEnvPath is the bundle's single credentials + settings file,
// $XDG_CONFIG_HOME/tui/env (default ~/.config/tui/env). Auth writes it; every
// `tui <app>` reads it, so a Homebrew install needs no source tree.
func UserEnvPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "tui", "env")
}

// LoadUserEnv applies UserEnvPath to the process environment, skipping keys
// already set so a real shell export (or a value the launcher injected) wins. A
// missing file is fine: it just means nothing is logged in yet.
func LoadUserEnv() {
	for k, v := range ParseEnvFile(UserEnvPath()) {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

// ParseEnvFile reads KEY=VALUE lines from a .env-style file (optional `export `,
// shell single/double quotes). A missing or unreadable file yields an empty map.
func ParseEnvFile(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
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
			out[k] = unquoteEnv(strings.TrimSpace(v))
		}
	}
	return out
}

// unquoteEnv undoes the shell quoting the auth writer uses (KEY='value', with an
// embedded ' escaped as '\''), and plain double quotes, leaving bare values.
func unquoteEnv(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], `'\''`, `'`)
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}
