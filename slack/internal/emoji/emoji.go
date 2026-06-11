// Package emoji fetches a workspace's custom emoji names from Slack so the TUI
// can fuzzy-search them when reacting.
//
// This is the ONE place slack-tui reads a Slack token value and calls the Slack
// API directly: slack-mcp-server (even latest) exposes no emoji-listing tool, so
// there is no other source for the custom names. It is a single read-only
// emoji.list call. Every reaction WRITE still goes through the server's
// reactions_add/reactions_remove tools, never from here.
package emoji

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const apiURL = "https://slack.com/api/emoji.list"

// browserUA is required for browser-session (xoxc/xoxd) tokens; Slack rejects the
// stealth flow without a browser-like User-Agent. Ignored for OAuth tokens.
const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36"

// List returns the workspace's custom emoji names, sorted. Names include alias
// entries (an alias name is itself reactable). Returns an error the caller can
// surface (e.g. missing_scope when an xoxp token lacks emoji:read).
func List(ctx context.Context) ([]string, error) {
	token, xoxd, err := credentials()
	if err != nil {
		return nil, err
	}
	req, err := buildRequest(ctx, token, xoxd)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseList(resp.Body)
}

// parseList decodes an emoji.list response into sorted names. Alias keys are
// kept: an alias name (e.g. "shipit") is itself reactable.
func parseList(r io.Reader) ([]string, error) {
	var out struct {
		OK    bool              `json:"ok"`
		Error string            `json:"error"`
		Emoji map[string]string `json:"emoji"` // name -> image URL or "alias:<target>"
	}
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding emoji.list: %w", err)
	}
	if !out.OK {
		return nil, fmt.Errorf("emoji.list: %s", out.Error)
	}

	names := make([]string, 0, len(out.Emoji))
	for name := range out.Emoji {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// buildRequest constructs the emoji.list request for the given token. A browser
// token (xoxc-) authenticates via the token form field plus the xoxd "d" cookie;
// an OAuth token (xoxp-/xoxb-) uses a Bearer header. Split out for testing.
func buildRequest(ctx context.Context, token, xoxd string) (*http.Request, error) {
	isClient := strings.HasPrefix(token, "xoxc-")

	form := url.Values{}
	if isClient {
		form.Set("token", token)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if isClient {
		// The xoxd value is already percent-encoded as the browser stores it;
		// pass it through verbatim. Re-encoding would corrupt it.
		req.AddCookie(&http.Cookie{Name: "d", Value: xoxd, Path: "/", Domain: ".slack.com", Secure: true})
		req.Header.Set("User-Agent", browserUA)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// credentials picks a token the same way the server does: prefer OAuth (xoxp,
// then xoxb), otherwise the browser-session pair. Returns the second value only
// for the browser pair (the xoxd cookie).
func credentials() (token, xoxd string, err error) {
	if t := os.Getenv("SLACK_MCP_XOXP_TOKEN"); t != "" {
		return t, "", nil
	}
	if t := os.Getenv("SLACK_MCP_XOXB_TOKEN"); t != "" {
		return t, "", nil
	}
	xoxc, xoxd := os.Getenv("SLACK_MCP_XOXC_TOKEN"), os.Getenv("SLACK_MCP_XOXD_TOKEN")
	if xoxc != "" && xoxd != "" {
		return xoxc, xoxd, nil
	}
	return "", "", errors.New("no Slack token available to list custom emoji")
}
