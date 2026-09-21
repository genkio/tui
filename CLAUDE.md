# tui

## Restarting the dev server after a change

The server runs in the foreground of a [Herdr](https://herdr.dev) pane, usually
the one below this agent's. Find it rather than guessing the id, which changes
between sessions:

```sh
herdr pane list --workspace "$HERDR_WORKSPACE_ID"   # the one whose cwd is this repo
herdr pane layout --pane "$HERDR_PANE_ID"           # which of them is below
```

Rebuild and restart it there, not in a fresh shell, so it keeps the terminal the
user is watching:

```sh
herdr pane send-keys <pane-id> ctrl+c
herdr pane run <pane-id> 'make serve'
herdr pane wait-output <pane-id> --match 'listening on' --timeout 180000
herdr pane read <pane-id> --source recent-unwrapped --lines 20
```

`make serve` builds `./tui` plus every plugin binary and runs the server the way
this machine runs it, `--sync-dir` and all. Another sync dir is
`make serve SYNC_DIR=...`; check the pane's scrollback if what it was running
looks different.

Do this whenever a change needs to be seen in the running web UI. No need to ask.

## "release"

When the user says "release", do the whole thing without asking again: commit the
pending changes, push, cut a GitHub release, and update the Homebrew tap. Every
release so far has been a minor bump (`v0.30.0` → `v0.31.0`), including
fix-only ones.

```sh
git add -A && git commit && git push          # conventional commit, no Co-Authored-By
V=0.31.0
git tag -a "v$V" -m "v$V" && git push origin "v$V"

for p in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
  GOOS=${p%/*} GOARCH=${p#*/} go build -ldflags "-X main.version=v$V" -o /tmp/rel/tui ./cmd/tui
  tar czf "dist/tui_${V}_${p%/*}_${p#*/}.tar.gz" -C /tmp/rel tui
done

gh release create "v$V" dist/tui_${V}_*.tar.gz --title "v$V" --notes "..."
```

Release notes are a hand-written bullet list of what changed for someone using
the app, not a commit log. Read `gh release view v0.30.0 --json body` for the
voice.

Then in `../homebrew-tap/Formula/tui.rb` swap all four URLs and `sha256` values
(`shasum -a 256 dist/tui_${V}_*.tar.gz`), commit as
`chore(tui): update to v$V`, and push.
