package core

import (
	"os"
	"testing"

	"github.com/chromedp/cdproto/network"
)

func TestUpsertUserEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := UpsertUserEnv(map[string]string{"XTUI_AUTH_TOKEN": "tok", "XTUI_CT0": "csrf"}); err != nil {
		t.Fatal(err)
	}
	got := ParseEnvFile(UserEnvPath())
	if got["XTUI_AUTH_TOKEN"] != "tok" || got["XTUI_CT0"] != "csrf" {
		t.Fatalf("after write: %v", got)
	}

	// Append a hand-written line, then upsert one key: it updates, the other key
	// survives, and the manual line is preserved.
	path := UserEnvPath()
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, []byte("# note\nOTHER=keep\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpsertUserEnv(map[string]string{"XTUI_AUTH_TOKEN": "tok2"}); err != nil {
		t.Fatal(err)
	}
	got = ParseEnvFile(path)
	if got["XTUI_AUTH_TOKEN"] != "tok2" {
		t.Errorf("token not updated: %q", got["XTUI_AUTH_TOKEN"])
	}
	if got["XTUI_CT0"] != "csrf" {
		t.Errorf("other key lost: %q", got["XTUI_CT0"])
	}
	if got["OTHER"] != "keep" {
		t.Errorf("manual line lost: %q", got["OTHER"])
	}
}

func TestUpsertUserEnvRoundTripsTrickyValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := UpsertUserEnv(map[string]string{"K": "a'b c=d"}); err != nil {
		t.Fatal(err)
	}
	if got := ParseEnvFile(UserEnvPath())["K"]; got != "a'b c=d" {
		t.Errorf("round-trip failed: %q, want %q", got, "a'b c=d")
	}
}

func TestSessionCookie(t *testing.T) {
	s := &Session{cookies: []*network.Cookie{
		{Name: "auth_token", Value: "AAA", Domain: ".x.com"},
		{Name: "auth_token", Value: "OLD", Domain: ".twitter.com"},
		{Name: "ct0", Value: "CCC", Domain: "x.com"},
		{Name: "sid", Value: "1", Domain: ".inoreader.com"},
		{Name: "pref", Value: "2", Domain: "www.inoreader.com"},
	}}
	if got := s.Cookie("auth_token", "x.com"); got != "AAA" {
		t.Errorf("Cookie(x.com) = %q, want AAA", got)
	}
	if got := s.Cookie("ct0"); got != "CCC" {
		t.Errorf("Cookie(any) = %q, want CCC", got)
	}
	if got := s.Cookie("nope"); got != "" {
		t.Errorf("missing cookie = %q, want empty", got)
	}
	if got := s.CookieHeader("inoreader.com"); got != "sid=1; pref=2" {
		t.Errorf("CookieHeader = %q, want 'sid=1; pref=2'", got)
	}
}
