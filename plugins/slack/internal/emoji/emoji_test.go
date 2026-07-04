package emoji

import (
	"context"
	"strings"
	"testing"
)

func TestBuildRequestOAuth(t *testing.T) {
	req, err := buildRequest(context.Background(), "xoxp-abc", "")
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer xoxp-abc" {
		t.Errorf("Authorization = %q, want Bearer xoxp-abc", got)
	}
	if len(req.Cookies()) != 0 {
		t.Errorf("OAuth request should carry no cookies, got %v", req.Cookies())
	}
}

func TestBuildRequestBrowserToken(t *testing.T) {
	req, err := buildRequest(context.Background(), "xoxc-123", "xoxd-cookie%2Fvalue")
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("browser token must not use a Bearer header (token rides in the body)")
	}
	if req.Header.Get("User-Agent") != browserUA {
		t.Error("browser token requires a browser User-Agent")
	}
	c, err := req.Cookie("d")
	if err != nil || c.Value != "xoxd-cookie%2Fvalue" {
		t.Errorf("d cookie = %v (err %v), want verbatim xoxd value (no re-encoding)", c, err)
	}
}

func TestParseList(t *testing.T) {
	names, err := parseList(strings.NewReader(
		`{"ok":true,"emoji":{"partyparrot":"https://x/p.gif","shipit":"alias:squirrel","catjam":"https://x/c.gif"}}`))
	if err != nil {
		t.Fatalf("parseList: %v", err)
	}
	// Sorted, and the alias key is kept (it is itself reactable).
	want := []string{"catjam", "partyparrot", "shipit"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestParseListError(t *testing.T) {
	if _, err := parseList(strings.NewReader(`{"ok":false,"error":"missing_scope"}`)); err == nil ||
		!strings.Contains(err.Error(), "missing_scope") {
		t.Errorf("expected missing_scope error, got %v", err)
	}
}
