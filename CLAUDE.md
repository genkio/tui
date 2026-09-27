# tui

## Restarting the server after a change

The server runs as a launchd agent (`com.genkio.tui`, installed by
`make service`), not in a terminal. After a change that needs to be seen in the
running web UI, rebuild and restart it:

```sh
make restart   # builds ./tui and every plugin, restarts, prints the log until "listening on"
```

No need to ask. Its output goes to `~/Library/Logs/tui.log` (`make logs` follows
it; the user may be watching that in a Herdr pane). If `make restart` says the
agent isn't loaded, run `make service`. Re-run `make service` too if the
Makefile's `service` target or `SYNC_DIR` changed, since the plist is written
from them.

`make serve` still runs it in the foreground, but only after
`make service-uninstall`, or the two fight over port 8080.

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
