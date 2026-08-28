# Feed server and client contract

## Goal

One long-running process owns feed fetching and `feed.db`. The terminal and web
interfaces are clients of that process, so reading an item in either interface
updates the same backlog and reaches the source through the same background
flusher.

## Commands

### Feed service

```sh
tui serve --sync-dir ~/box/tui
```

The service:

- opens the local `feed.db` and restores it from the sync directory when the
  local database does not exist;
- reads credentials and configuration from the sync directory;
- fetches once at startup and then on the configured interval;
- records reads immediately and flushes them to each source in the background;
- snapshots `feed.db` to the sync directory after a fetch; and
- serves the JSON API and HTML web application from the same address.

Server options:

```sh
tui serve --sync-dir ~/box/tui --addr 0.0.0.0:8080
tui serve --sync-dir ~/box/tui --fetch-every 20m
tui serve --sync-dir ~/box/tui --fetch-every 0
```

The default address is `0.0.0.0:8080`. The default fetch interval is ten
minutes with jitter. Inoreader draining is enabled by default.

The old `--web`, `--web-addr`, `--web-fetch`, and `--web-drain` flags are
removed. This is a private application, so no compatibility alias is needed.

### Terminal feed

```sh
tui
tui all
tui --server http://100.121.244.89:8080
tui all --server http://100.121.244.89:8080
```

`tui` defaults to the merged All feed. `tui all` is its explicit spelling. The
terminal client:

- loads items, counts, and source status from the feed service;
- sends read and unread actions to the service;
- reloads the service cache when the user asks to refresh;
- does not fetch source plugins directly;
- does not open `feed.db`; and
- does not need `--sync-dir`.

The server URL is resolved in this order:

1. `--server URL`;
2. `TUI_SERVER_URL`; and
3. `http://127.0.0.1:8080`.

If the service cannot be reached, the terminal reports how to start or select
one. It does not fall back to direct fetching.

### Web client

```sh
tui web
tui web --server http://100.121.244.89:8080
```

This command opens the service URL in the local browser and exits. It does not
start a server. A phone or another computer can open the same URL directly.

### Individual applications

```sh
tui x
tui reddit
tui inoreader
tui folo
tui douban
tui bilibili
tui slack
```

Individual applications remain standalone in the first implementation. They
may use `--sync-dir` for credentials, configuration, and their own state:

```sh
tui x --auth --sync-dir ~/box/tui
tui reddit --sync-dir ~/box/tui
```

Only the merged terminal feed is converted into a service client.

## Ownership and synchronization

`tui serve` is the sole owner of feed state and the only process that writes
the feed backlog. Clients never modify SQLite directly because the service also
holds an in-memory cache that must agree with the database.

Both interfaces send mutations through the service. A web read appears in the
terminal after reload, and a terminal read appears in the web interface after
reload. Live push synchronization is out of scope because the interfaces are
not expected to be used concurrently.

The service may accept multiple clients. It remains the only fetcher, upstream
read flusher, and Dropbox snapshot writer regardless of how many clients are
connected.

X For You remains a live service request and is not added to the accumulated
backlog unless it is saved. Persisting tagged or otherwise labelled For You
items is separate work.

## Remote access

A service listening on `0.0.0.0:8080` is reachable through the host's Tailscale
address, subject to the operating-system firewall and Tailscale ACLs. Tailscale
encrypts the connection even when the application URL uses `http`.

The service has no application-level authentication in this version. Any
device allowed to reach the port can read and mutate the feed. Restricting the
listener or the Tailscale ACL is therefore part of deployment.
