# slack-tui

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Built with Bubble Tea](https://img.shields.io/badge/built%20with-Bubble%20Tea-ff69b4)](https://github.com/charmbracelet/bubbletea)

A terminal UI for triaging your unread Slack. It does one job well: pull your
unread DMs and channels, let you read them (threads included), and mark them
read. Keyboard-driven, single binary, no browser.

It is a thin [MCP](https://modelcontextprotocol.io) client on top of
[`korotovsky/slack-mcp-server`](https://github.com/korotovsky/slack-mcp-server),
which already solves Slack auth, unread aggregation, and mark-as-read. slack-tui
never touches the Slack API or your tokens directly.

```
+-------------+   stdio (MCP / JSON-RPC)   +-------------------+   HTTPS   +-------+
|  slack-tui  | <------------------------> | slack-mcp-server  | <-------> | Slack |
| (MCP client)|    spawned as a child      |   (korotovsky)    |           |  API  |
+-------------+                            +-------------------+           +-------+
```

## Features

- One screen listing every unread conversation (DMs, group DMs, channels) with
  counts, ordered by the server's priority (DMs first).
- Open a conversation, read its messages with author and local time, and expand
  threads inline.
- Mark a conversation read from the list or from the detail view; the list
  refreshes and the item drops off.
- React to a message with your workspace's custom emoji: fuzzy-search them by
  name and toggle a reaction on or off.
- On-demand refresh, a "last updated" indicator, filtering, and a help bar.
- Tokens are never logged, printed, or written to disk by slack-tui.

## Requirements

- **macOS or Linux.**
- **Node.js** (for the default `npx` launch of the server) **or Docker**.
- **Go 1.26+** to build from source.

## Install

Ships as part of [`tui`](../../README.md) (`brew install genkio/tap/tui`, then
`tui slack`). To build this app on its own from a source checkout:

```bash
cd plugins/slack
make build            # produces ./slack-tui
# or: make install    # into $GOBIN / $GOPATH/bin
```

## Authentication

The server needs a Slack token. Pick one mode and export it before running
slack-tui, which forwards it to the spawned server. **slack-tui only checks that
a token is present; it never reads the value.**

| Mode | Env vars | Notes |
| --- | --- | --- |
| **OAuth user token** (recommended) | `SLACK_MCP_XOXP_TOKEN=xoxp-...` | Create an internal Slack app, add user read scopes, install it, copy the user token. Cleanest for corporate workspaces. |
| **Browser session** ("stealth") | `SLACK_MCP_XOXC_TOKEN=xoxc-...` + `SLACK_MCP_XOXD_TOKEN=xoxd-...` | Reuses your logged-in desktop/web session. Full feature parity (including `mentions_only`). Gray area under Slack's terms; the `xoxd` cookie expires roughly yearly. |
| Bot token | `SLACK_MCP_XOXB_TOKEN=xoxb-...` | Limited: cannot list unreads. Not recommended for this tool. |

See the [server's authentication docs](https://github.com/korotovsky/slack-mcp-server/blob/master/docs/01-authentication-setup.md)
for exactly how to obtain each token. Confirm your workspace's policy allows the
mode you pick.

### Extracting browser-session tokens (xoxc + xoxd)

**Easy way:** `tui slack --auth` (or `make auth` from a checkout) opens a
Chromium-family browser, waits for you to log into your workspace, then saves
`SLACK_MCP_XOXC_TOKEN`, `SLACK_MCP_XOXD_TOKEN`, and the workspace domain to
`~/.config/tui/env`. Re-run when the session expires.

**Manual way:** the browser-session mode needs **both** values, and they live in
different places. Grab them from the browser where you are logged into Slack
(Brave and other Chromium browsers work too). Open `https://app.slack.com` and
open DevTools (`Cmd+Option+I` on macOS, `F12` elsewhere).

1. **`xoxc` token** (Console tab). The console blocks pasted code until you type
   `allow pasting` once and press enter. Then run:

   ```js
   Object.values(JSON.parse(localStorage.localConfig_v2).teams)
     .forEach(t => console.log(t.name, '=>', t.token))
   ```

   Copy the `xoxc-...` token for the workspace you want.

2. **`xoxd` token** (Application -> Storage -> Cookies -> `https://app.slack.com`).
   Copy the **Value** of the cookie named **`d`** (it starts with `xoxd-`). This
   cookie is `HttpOnly`, so it never shows up in console output; the cookie panel
   is the only place to read it.

```bash
export SLACK_MCP_XOXC_TOKEN='xoxc-...'
export SLACK_MCP_XOXD_TOKEN='xoxd-...'
```

If auth fails and the cookie value contains `%` escapes, URL-decode it
(`%2F` -> `/`, `%2B` -> `+`, `%3D` -> `=`).

### Enable mark-as-read

The server ships with mark-as-read **disabled**. Turn it on (slack-tui forwards
this and reads it to know whether marking is available):

```bash
export SLACK_MCP_MARK_TOOL=true
```

Without it, reading still works; pressing `r` shows a reminder instead of marking.

### Enable reactions

Reactions are **disabled** on the server by default too. Turn them on:

```bash
export SLACK_MCP_REACTION_TOOL=true
```

Then in the detail view press `e` on a message to open the emoji picker, type to
fuzzy-search, and press enter to add the reaction; pressing the same emoji again
removes it. The value also accepts a channel allowlist (`C123,D456`) or an
exclusion (`!C123`) instead of `true`.

To fuzzy-search your workspace's **custom** emoji (the ones your coworkers
uploaded), slack-tui fetches their names with a single read-only Slack
`emoji.list` call. An `xoxp` token needs the `emoji:read` scope for this;
browser-session (`xoxc`/`xoxd`) tokens work as-is. If the list can't be fetched
you can still type an exact emoji name and react. See the [security note](#security-notes).

## Quick start

```bash
export SLACK_MCP_XOXP_TOKEN=xoxp-your-token
export SLACK_MCP_MARK_TOOL=true

slack-tui --check     # verify the connection and list the server's tools
slack-tui             # launch the TUI
```

The first run downloads the server via `npx` and may take a moment.

To avoid re-exporting tokens every session, copy the sample env file and source it:

```bash
tui slack --auth             # browser-session mode: log in once; writes to ~/.config/tui/env
tui slack --check            # verify the connection
tui slack                    # launch the TUI
```

`~/.config/tui/env` is read on every run. For the `xoxp` mode, put that token
there (or export it in your shell) instead of running `--auth`.

## Keybindings

| Key | Action |
| --- | --- |
| `j` / `↓`, `k` / `↑` | Move down / up |
| `enter` / `l` | Open conversation (in detail: expand/collapse the thread at the cursor) |
| `space` | Expand/collapse the full text of the selected message (detail view) |
| `esc` / `h` / `backspace` | Back to the list |
| `o` | Render the message link in [carbonyl](https://github.com/genkio/carbonyl) inside the terminal; `q` quits back (detail view) |
| `O` | Same as `o` but rendered with `--graphics` (kitty graphics protocol) |
| `b` | Open the selected message/thread in your browser (detail view) |
| `e` | React to the selected message: fuzzy-search custom emoji, enter toggles it (detail view) |
| `r` | Mark read (list: highlighted conversation; detail: up to the latest message) |
| `R` | Refresh unreads |
| `g` / `G` | Jump to top / bottom |
| `/` | Filter the list |
| `?` | Toggle full help |
| `q` / `ctrl+c` | Quit |

## Configuration

Everything has a sensible default. To override, create
`~/.config/slack-tui/config.toml` (see [`config.example.toml`](config.example.toml))
or pass `--config <path>`. Environment variables win over the file.

### Settings vs. tokens

slack-tui takes two kinds of input, loaded differently:

- **Settings** (theme, refresh, workspace domain, channel filters, page sizes) are
  read from `config.toml` automatically at startup, from any directory. Precedence:
  flag > `SLACK_TUI_*` env var > file > default. No sourcing or wrapper needed.
- **Tokens** (`SLACK_MCP_*`, plus `SLACK_MCP_MARK_TOOL`) are secrets, so they are
  never stored in `config.toml`. They must be present in the environment when
  slack-tui runs; it forwards them to the spawned server.

So if you installed the binary (`make install` / `go install`), put your settings
in `~/.config/slack-tui/config.toml` and load your tokens however you prefer, in
`~/.zshrc`, a secrets manager, the macOS Keychain, or a sourced file:

```sh
# ~/.zshrc
export SLACK_MCP_XOXC_TOKEN=xoxc-...
export SLACK_MCP_XOXD_TOKEN=xoxd-...
export SLACK_MCP_MARK_TOOL=true
```

Then `tui slack` works from any directory. `--auth` writes the same tokens to
`~/.config/tui/env`, which is read on every run; either is fine.

**Server auth (forwarded to slack-mcp-server):** `SLACK_MCP_XOXP_TOKEN`,
`SLACK_MCP_XOXB_TOKEN`, `SLACK_MCP_XOXC_TOKEN`, `SLACK_MCP_XOXD_TOKEN`,
`SLACK_MCP_MARK_TOOL`, `SLACK_MCP_REACTION_TOOL`.

**slack-tui options:**

| Variable | Default | Meaning |
| --- | --- | --- |
| `SLACK_TUI_SERVER_COMMAND` | `npx` | Executable that launches the server |
| `SLACK_TUI_SERVER_ARGS` | `-y slack-mcp-server@1.3.0 --transport stdio` | Args for it (space-separated) |
| `SLACK_TUI_CHANNEL_TYPES` | `all` | `all`/`dm`/`group_dm`/`partner`/`internal` |
| `SLACK_TUI_MAX_CHANNELS` | `50` | Max conversations to list |
| `SLACK_TUI_MAX_MESSAGES` | `10` | Max messages per conversation in the summary |
| `SLACK_TUI_MENTIONS_ONLY` | `false` | Only conversations that mention you (xoxc/xoxd only) |
| `SLACK_TUI_THEME` | `auto` | `auto` (match the terminal background), `light`, or `dark` |
| `SLACK_TUI_SLACK_DOMAIN` | (unset) | Workspace subdomain (e.g. `acme`), used to build message links for `o`/`b` |
| `SLACK_TUI_REFRESH` | (off) | Auto-refresh interval, e.g. `30s`/`2m`. The `--refresh` flag overrides it. |
| `XDG_CONFIG_HOME` | `~/.config` | Where the config file lives |

Colors adapt to your terminal's background automatically. If detection misbehaves
(some terminals do not answer the background query), force it with
`SLACK_TUI_THEME=light` or `dark`.

### Running the server in Docker instead of npx

Pin a version (or a digest) and pass auth via `-e` references to your host env:

```toml
[server]
command = "docker"
args = ["run", "-i", "--rm",
        "-e", "SLACK_MCP_XOXP_TOKEN", "-e", "SLACK_MCP_MARK_TOOL",
        "ghcr.io/korotovsky/slack-mcp-server:1.3.0", "--transport", "stdio"]
```

## Flags

- `--check` connect, print the server's tools and a readiness summary, exit.
- `--version` print the version.
- `--config <path>` use a specific config file.
- `--refresh <duration>` auto-refresh the unread list on an interval, e.g. `--refresh 30s` or `--refresh 2m` (off by default). The list re-fetches in the background and keeps your current selection.

## Troubleshooting

- **"Slack rejected the token"** the token is wrong or expired. Re-check your
  `SLACK_MCP_*` env vars; re-extract `xoxc`/`xoxd` if you use browser tokens.
- **`r` says mark-as-read is off** set `SLACK_MCP_MARK_TOOL=true` and restart.
- **`o`/`b` says no link available** Slack omits permalinks from message history, so
  slack-tui builds them from your workspace. Set `SLACK_TUI_SLACK_DOMAIN` to your
  subdomain (the `acme` in `acme.slack.com`).
- **First launch hangs for a while** `npx` is downloading the server. It is
  cached afterward. Run `slack-tui --check` to see progress and any error.
- **Want server logs?** Connection and tool errors include the server's own
  output (with token values redacted) in the error message.

## Security notes

- slack-mcp-server is third-party code that can see every message you can. Pin a
  specific version or Docker digest (the default pins `@1.3.0`) and review it
  before pointing it at a sensitive workspace.
- slack-tui's only writes are mark-as-read and emoji reactions, both done through
  the server's own tools and both off until you enable them. The server's
  message-posting tool is left disabled.
- One exception to "never call Slack directly": to fuzzy-search custom emoji,
  slack-tui makes a single read-only `emoji.list` request using the token already
  in your environment, to fetch custom-emoji names. The token value is read only
  for that one call; reactions and everything else still go through the server.
- Never commit tokens. Creds live in `~/.config/tui/env` (0600); the login
  browser profile is under `~/.config/tui/profile`.

## License

MIT. See [`LICENSE`](LICENSE).

## Acknowledgements

- [korotovsky/slack-mcp-server](https://github.com/korotovsky/slack-mcp-server) does the hard Slack work.
- Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), and [Lip Gloss](https://github.com/charmbracelet/lipgloss).
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) for the MCP client.
