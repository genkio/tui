package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/genkio/tui/core"
)

// gated serves the path through a gate in front of a handler that answers 200,
// from the given remote address (a tunnel's is 127.0.0.1 plus its headers).
func gated(a *webAuth, method, target, remote string, header http.Header, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isGuest(r.Context()) {
			w.Header().Set("X-Guest", "1")
		}
	})).ServeHTTP(rec, req)
	return rec
}

var tunneled = http.Header{"X-Forwarded-For": {"203.0.113.9"}, "Accept": {"text/html"}}

func cookieFrom(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie set", name)
	return nil
}

func TestDirectOnlyWithoutARelay(t *testing.T) {
	tailnetLocal := &net.TCPAddr{IP: net.ParseIP("100.100.1.1"), Port: 8080}
	lanLocal := &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 8080}
	for _, tc := range []struct {
		name   string
		remote string
		local  net.Addr
		header http.Header
		want   bool
	}{
		{"terminal client on this machine", "127.0.0.1:5000", nil, nil, true},
		{"ipv6 loopback", "[::1]:5000", nil, nil, true},
		{"tailnet peer", "100.101.2.3:5000", tailnetLocal, nil, true},
		{"tailnet peer over ipv6", "[fd7a:115c:a1e0::5]:5000", tailnetLocal, nil, true},
		{"tunnel from loopback", "127.0.0.1:5000", nil, http.Header{"X-Forwarded-For": {"203.0.113.9"}}, false},
		{"tailscale serve", "127.0.0.1:5000", nil, http.Header{"X-Forwarded-Host": {"mac.tail.ts.net"}}, false},
		{"cloudflared", "127.0.0.1:5000", nil, http.Header{"Cf-Connecting-Ip": {"203.0.113.9"}}, false},
		{"lan", "192.168.1.9:5000", lanLocal, nil, false},
		{"cgnat on a lan, not the tailnet", "100.101.2.3:5000", lanLocal, nil, false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = tc.remote
		for k, v := range tc.header {
			req.Header[k] = v
		}
		if tc.local != nil {
			req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, tc.local))
		}
		if got := direct(req); got != tc.want {
			t.Errorf("%s: direct = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGateAsksARelayedPageLoadToSignIn(t *testing.T) {
	a := newWebAuth("hunter2")
	rec := gated(a, http.MethodGet, "/?saved=1", "127.0.0.1:5000", tunneled)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `name="password"`) || !strings.Contains(rec.Body.String(), `value="/?saved=1"`) {
		t.Errorf("a page load should get the form, pointing back where it was going: %s", rec.Body.String())
	}
	if rec := gated(a, http.MethodPost, "/mark", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"203.0.113.9"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a fetch should be refused, got %d", rec.Code)
	}
	if rec := gated(a, http.MethodGet, "/", "127.0.0.1:5000", nil); rec.Code != http.StatusOK {
		t.Errorf("the terminal client on this machine should not need the password, got %d", rec.Code)
	}
	if rec := gated(a, http.MethodGet, "/manifest.webmanifest", "127.0.0.1:5000", tunneled); rec.Code != http.StatusOK {
		t.Errorf("what installs the page is public, got %d", rec.Code)
	}
}

func TestLoginSetsACookieThatOpensEverything(t *testing.T) {
	a := newWebAuth("hunter2")
	post := func(pw, next string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"password": {pw}, "next": {next}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		req.RemoteAddr = "127.0.0.1:5000"
		rec := httptest.NewRecorder()
		a.wrap(http.NotFoundHandler()).ServeHTTP(rec, req)
		return rec
	}
	if rec := post("wrong", "/"); rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("a wrong password: status %d, cookies %v", rec.Code, rec.Result().Cookies())
	}
	rec := post("hunter2", "//evil.example/")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Errorf("redirect = %d %q, want 303 to / (never off-site)", rec.Code, rec.Header().Get("Location"))
	}
	session := cookieFrom(t, rec, sessionCookie)
	if !session.HttpOnly {
		t.Error("the session cookie should be out of a script's reach")
	}
	if rec := gated(a, http.MethodPost, "/mark", "127.0.0.1:5000", tunneled, session); rec.Code != http.StatusOK {
		t.Errorf("signed in, got %d", rec.Code)
	}
	if rec := gated(newWebAuth("changed"), http.MethodGet, "/", "127.0.0.1:5000", tunneled, session); rec.Code != http.StatusUnauthorized {
		t.Errorf("a new password should sign every browser out, got %d", rec.Code)
	}
	forged := &http.Cookie{Name: sessionCookie, Value: "9999999999.AAAA"}
	if rec := gated(a, http.MethodGet, "/", "127.0.0.1:5000", tunneled, forged); rec.Code != http.StatusUnauthorized {
		t.Errorf("a forged cookie got %d", rec.Code)
	}
}

func TestExpiredCookieIsRefused(t *testing.T) {
	a := newWebAuth("hunter2")
	rec := httptest.NewRecorder()
	a.setCookie(rec, httptest.NewRequest(http.MethodGet, "/", nil), sessionCookie, -time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookieFrom(t, rec, sessionCookie).Value})
	if a.validCookie(req, sessionCookie) {
		t.Error("an expired cookie should not count")
	}
}

func TestLoginStopsListeningAfterTooManyWrongPasswords(t *testing.T) {
	a := newWebAuth("hunter2")
	for range loginFailMax {
		a.recordFail()
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("password=hunter2"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.login(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 even for the right password", rec.Code)
	}
}

func TestSharedLinkOpensOneItemAndItsMedia(t *testing.T) {
	a := newWebAuth("hunter2")
	link := a.itemLink("reddit", "77")
	if !strings.HasPrefix(link, "/item?app=reddit&id=77&share=") {
		t.Fatalf("link = %q", link)
	}
	rec := gated(a, http.MethodGet, link, "127.0.0.1:5000", tunneled)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Guest") != "1" {
		t.Fatalf("the shared link: status %d, guest %q", rec.Code, rec.Header().Get("X-Guest"))
	}
	guest := cookieFrom(t, rec, guestCookie)

	other := strings.Replace(link, "id=77", "id=78", 1)
	if rec := gated(a, http.MethodGet, other, "127.0.0.1:5000", tunneled, guest); rec.Code != http.StatusUnauthorized {
		t.Errorf("the signature is for one item, but another opened with %d", rec.Code)
	}
	if rec := gated(a, http.MethodGet, "/img?u=https://img1.doubanio.com/a.jpg", "127.0.0.1:5000", tunneled, guest); rec.Code != http.StatusOK {
		t.Errorf("the shared page's pictures should load, got %d", rec.Code)
	}
	for _, path := range []string{"/?saved=1", "/status", "/item?app=reddit&id=77"} {
		if rec := gated(a, http.MethodGet, path, "127.0.0.1:5000", tunneled, guest); rec.Code != http.StatusUnauthorized {
			t.Errorf("a guest reached %s with %d", path, rec.Code)
		}
	}
	if rec := gated(a, http.MethodPost, "/save", "127.0.0.1:5000", tunneled, guest); rec.Code != http.StatusUnauthorized {
		t.Errorf("a guest saved with %d", rec.Code)
	}
	if rec := gated(a, http.MethodGet, link, "127.0.0.1:5000", nil); rec.Header().Get("X-Guest") != "" {
		t.Error("the owner following their own link is still the owner")
	}
}

func TestGuestItemPageShowsNoneOfTheReader(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{{App: "reddit", ID: "77", Title: "a post of its own"}}, time.Now())
	loader, err := newPageLoader("")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/item?app=reddit&id=77", nil)
	req = req.WithContext(context.WithValue(req.Context(), guestKey{}, true))
	rec := httptest.NewRecorder()
	handleItem(rec, req, loader, cache, nil, nil, newRenderedItems())
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "a post of its own") {
		t.Fatalf("status %d: %s", rec.Code, body)
	}
	if !strings.Contains(body, `data-guest="true"`) {
		t.Error("the page's script should know it is a guest's")
	}
	for _, id := range []string{`id="filters"`, `id="settingsdlg"`, `href="/?saved=1"`} {
		if strings.Contains(body, id) {
			t.Errorf("a guest's page carries %s", id)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "/",
		"/?saved=1":         "/?saved=1",
		"//evil.example":    "/",
		"/\\evil.example":   "/",
		"https://evil.test": "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShareButtonSendsTheSignedPageOnlyBehindAPassword(t *testing.T) {
	cache := newTestCache(t)
	cache.upsert([]core.Item{{App: "reddit", ID: "77", Title: "a post of its own", URL: "https://reddit.com/r/x/77"}}, time.Now())

	if body := getItem(t, "app=reddit&id=77", cache, nil, nil).Body.String(); strings.Contains(body, "data-link=") {
		t.Error("with no password the share button should send the original")
	}

	webGate = newWebAuth("hunter2")
	t.Cleanup(func() { webGate = nil })
	body := getItem(t, "app=reddit&id=77", cache, nil, nil).Body.String()
	want := `data-link="` + strings.ReplaceAll(webGate.itemLink("reddit", "77"), "&", "&amp;") + `"`
	if !strings.Contains(body, want) {
		t.Errorf("the share button should carry %s", want)
	}
}
