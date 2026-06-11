#!/usr/bin/env bash
# Capture a logged-in browser session cookie into an env file using playwright-cli.
# Opens a real browser window (persistent profile, so re-login is rare), waits
# for you to log in, then writes the cookie header to the env file.
#
#   tools/auth.sh <session> <url> <domain> <ENVVAR> <envfile> [profile-dir]
#
# Example (Inoreader):
#   tools/auth.sh inoreader https://www.inoreader.com/all_articles inoreader.com \
#       INOREADER_COOKIE .env
set -euo pipefail

SESSION="${1:?session name}"
URL="${2:?url to open}"
DOMAIN="${3:?cookie domain, e.g. inoreader.com}"
VAR="${4:?env var name}"
ENVFILE="${5:?env file path}"
PROFILE="${6:-$HOME/.config/$SESSION/pw-profile}"
HERE="$(cd "$(dirname "$0")" && pwd)"

command -v playwright-cli >/dev/null || { echo "playwright-cli not found on PATH"; exit 1; }
command -v jq >/dev/null || { echo "jq not found on PATH"; exit 1; }

mkdir -p "$PROFILE"
echo "Opening a browser for '$SESSION' (persistent profile: $PROFILE)..."
playwright-cli -s="$SESSION" open "$URL" --headed --profile="$PROFILE" >/dev/null 2>&1 || true

echo
echo "  1. Log in to the site in the browser window that just opened."
echo "  2. Make sure you reach the logged-in page."
echo "  3. Come back here and press Enter to capture the cookie."
read -r _

JSON="$(playwright-cli -s="$SESSION" cookie-list --domain="$DOMAIN" --json 2>/dev/null || true)"
COOKIE="$(printf '%s' "$JSON" | jq -r '.result // empty' | sed -E 's/ \(domain.*$//' | paste -sd';' - | sed 's/;/; /g')"
if [ -z "$COOKIE" ]; then
  echo "No cookies captured for $DOMAIN. Are you logged in? Leaving $ENVFILE untouched."
  exit 1
fi

CAP_VALUE="$COOKIE" node "$HERE/upsert-env.mjs" "$ENVFILE" "$VAR"
playwright-cli -s="$SESSION" close >/dev/null 2>&1 || true
echo "Done. Re-run this script when the session expires."
