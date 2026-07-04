package core

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// userConfigDir is $XDG_CONFIG_HOME, else ~/.config; "" if the home is unknown.
func userConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config")
}

// UserEnvPath is the bundle's single credentials + settings file,
// $XDG_CONFIG_HOME/tui/env (default ~/.config/tui/env). Auth writes it; every
// `tui <app>` reads it, so a Homebrew install needs no source tree.
func UserEnvPath() string {
	dir := userConfigDir()
	if dir == "" {
		return ""
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

// UpsertUserEnv writes each var into UserEnvPath, replacing the existing line for
// a key or appending it and leaving other lines untouched. Values are
// single-quoted (shell-safe, matching what LoadUserEnv reads back); the file is
// 0600 since it holds session tokens.
func UpsertUserEnv(vars map[string]string) error {
	path := UserEnvPath()
	if path == "" {
		return errors.New("cannot locate a config dir (no HOME/XDG_CONFIG_HOME)")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys) // stable file order across runs
	for _, k := range keys {
		line := "export " + k + "=" + shellQuote(vars[k])
		re := regexp.MustCompile(`^\s*(export\s+)?` + regexp.QuoteMeta(k) + `=`)
		found := false
		for i, l := range lines {
			if re.MatchString(l) {
				lines[i] = line
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, line)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// shellQuote single-quotes v the way sh needs, escaping any embedded quote as
// '\'' so the value round-trips through unquoteEnv.
func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// unquoteEnv undoes the shell quoting shellQuote writes (KEY='value', with an
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
