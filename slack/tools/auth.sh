#!/usr/bin/env bash
# Capture Slack browser-session tokens (xoxc + xoxd) into an env file using
# playwright-cli. Opens a real browser (persistent profile, so re-login is rare),
# waits for you to log into your workspace, then writes both tokens.
#
#   tools/auth.sh [session] [envfile] [workspace-filter] [profile-dir]
#
# workspace-filter: optional substring of the workspace name/domain to pick when
# you are signed into more than one (default: the first one found).
set -euo pipefail

SESSION="${1:-slack}"
ENVFILE="${2:-.env}"
WS_FILTER="${3:-}"
PROFILE="${4:-$HOME/.config/slack-tui/pw-profile}"
HERE="$(cd "$(dirname "$0")" && pwd)"

command -v playwright-cli >/dev/null || { echo "playwright-cli not found on PATH"; exit 1; }
command -v jq >/dev/null || { echo "jq not found on PATH"; exit 1; }
command -v node >/dev/null || { echo "node not found on PATH"; exit 1; }

mkdir -p "$PROFILE"
echo "Opening a browser for Slack (persistent profile: $PROFILE)..."
playwright-cli -s="$SESSION" open "https://app.slack.com/client" --headed --profile="$PROFILE" >/dev/null 2>&1 || true

echo
echo "  1. Log into your Slack workspace in the browser window."
echo "  2. Wait until the workspace has fully loaded."
echo "  3. Press Enter here to capture the tokens."
read -r _

# xoxd: the HttpOnly 'd' cookie (value starts xoxd-). Cookie panel is the only
# place this lives, which is why a browser capture beats copying by hand.
XOXD="$(playwright-cli -s="$SESSION" cookie-list --domain=slack.com --json 2>/dev/null \
  | jq -r '.result // empty' | sed -nE 's/^d=([^ ]+).*/\1/p' | head -1)"

# xoxc token + workspace domain come from localStorage.localConfig_v2. The
# post-login URL is app.slack.com/client/<TEAM_ID>/... with no subdomain, so the
# domain (e.g. "acme") has to come from the team object, not the URL.
# --raw eval returns a JSON-encoded string, so the first `jq -r .` unwraps it.
RAW="$(playwright-cli -s="$SESSION" --raw eval \
  "JSON.stringify(Object.values((JSON.parse(localStorage.localConfig_v2||'{}').teams)||{}).map(function(t){return {name:t.name,domain:t.domain,url:t.url,token:t.token}}))" 2>/dev/null || true)"
TEAMS="$(printf '%s' "$RAW" | jq -r '.' 2>/dev/null || echo '[]')"
N="$(printf '%s' "$TEAMS" | jq 'length' 2>/dev/null || echo 0)"

if [ "${N:-0}" -eq 0 ]; then
  echo "No Slack workspace found in localStorage. Are you fully logged in?"
  exit 1
fi
echo "Workspaces found:"
printf '%s' "$TEAMS" | jq -r '.[] | "  - \(.name) (\(.domain // .url // "?"))"'

if [ -n "$WS_FILTER" ]; then
  TEAM="$(printf '%s' "$TEAMS" | jq -c --arg f "$WS_FILTER" \
    '[.[]|select(((.name//"")|ascii_downcase|contains($f|ascii_downcase)) or ((.domain//"")|ascii_downcase|contains($f|ascii_downcase)))][0] // empty')"
else
  TEAM="$(printf '%s' "$TEAMS" | jq -c '.[0] // empty')"
fi
[ -n "$TEAM" ] || { echo "Could not select a workspace (filter: '${WS_FILTER:-none}')."; exit 1; }

XOXC="$(printf '%s' "$TEAM" | jq -r '.token // empty')"
# SlackBaseURL() accepts a bare subdomain or a full URL, so either field works.
DOMAIN="$(printf '%s' "$TEAM" | jq -r '.domain // .url // empty')"

[ -n "$XOXC" ] || { echo "Could not read the workspace token (xoxc)."; exit 1; }
[ -n "$XOXD" ] || { echo "Could not read the 'd' cookie (xoxd). Are you logged in?"; exit 1; }

CAP_VALUE="$XOXC" node "$HERE/upsert-env.mjs" "$ENVFILE" SLACK_MCP_XOXC_TOKEN
CAP_VALUE="$XOXD" node "$HERE/upsert-env.mjs" "$ENVFILE" SLACK_MCP_XOXD_TOKEN
if [ -n "$DOMAIN" ]; then
  CAP_VALUE="$DOMAIN" node "$HERE/upsert-env.mjs" "$ENVFILE" SLACK_TUI_SLACK_DOMAIN
fi
playwright-cli -s="$SESSION" close >/dev/null 2>&1 || true

echo "Done. Wrote SLACK_MCP_XOXC_TOKEN, SLACK_MCP_XOXD_TOKEN${DOMAIN:+, SLACK_TUI_SLACK_DOMAIN} to $ENVFILE."
echo "If auth later fails and the xoxd value contains % escapes, URL-decode it"
echo "(%2F -> /, %2B -> +, %3D -> =)."
