#!/usr/bin/env bash
# Capture x.com browser-session credentials (auth_token + ct0) into an env file
# using playwright-cli. Opens a real browser (persistent profile, so re-login is
# rare), waits for you to log into x.com, then writes both values.
#
#   tools/auth.sh [session] [envfile] [profile-dir]
#
# The web app's GraphQL API authenticates with the auth_token cookie plus the
# ct0 cookie echoed back in the x-csrf-token header. The Bearer is a public
# constant baked into the client, so it is not captured here.
set -euo pipefail

SESSION="${1:-x}"
ENVFILE="${2:-.env}"
PROFILE="${3:-$HOME/.config/x-tui/pw-profile}"
HERE="$(cd "$(dirname "$0")" && pwd)"

command -v playwright-cli >/dev/null || { echo "playwright-cli not found on PATH"; exit 1; }
command -v jq >/dev/null || { echo "jq not found on PATH"; exit 1; }
command -v node >/dev/null || { echo "node not found on PATH"; exit 1; }

mkdir -p "$PROFILE"
echo "Opening a browser for x.com (persistent profile: $PROFILE)..."
playwright-cli -s="$SESSION" open "https://x.com/home" --headed --profile="$PROFILE" >/dev/null 2>&1 || true

echo
echo "  1. Log into x.com in the browser window."
echo "  2. Wait until your home timeline has loaded."
echo "  3. Press Enter here to capture the session."
read -r _

# auth_token + ct0 live as cookies on x.com (older sessions: twitter.com), so
# query both and merge. cookie-list --json yields lines: 'name=value (domain=...)'.
read_cookies() { playwright-cli -s="$SESSION" cookie-list --domain="$1" --json 2>/dev/null | jq -r '.result // empty'; }
COOKIES="$(printf '%s\n%s' "$(read_cookies x.com)" "$(read_cookies twitter.com)")"

AUTH_TOKEN="$(printf '%s' "$COOKIES" | sed -nE 's/^auth_token=([^ ]+).*/\1/p' | head -1)"
CT0="$(printf '%s' "$COOKIES" | sed -nE 's/^ct0=([^ ]+).*/\1/p' | head -1)"

[ -n "$AUTH_TOKEN" ] || { echo "Could not read auth_token. Fully logged in?"; exit 1; }
[ -n "$CT0" ] || { echo "Could not read ct0 (CSRF cookie). Fully logged in?"; exit 1; }

CAP_VALUE="$AUTH_TOKEN" node "$HERE/upsert-env.mjs" "$ENVFILE" XTUI_AUTH_TOKEN
CAP_VALUE="$CT0" node "$HERE/upsert-env.mjs" "$ENVFILE" XTUI_CT0
playwright-cli -s="$SESSION" close >/dev/null 2>&1 || true

echo "Done. Wrote XTUI_AUTH_TOKEN and XTUI_CT0 to $ENVFILE."
echo "Re-run when the session expires (auth_token is long-lived, but not forever)."
