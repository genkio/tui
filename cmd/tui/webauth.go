package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"html/template"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// webAuth puts the web UI behind a password once TUI_WEB_PASSWORD is set, for
// when the server is reachable from the open internet (a Cloudflare tunnel,
// tailscale funnel) and not only from the tailnet.
//
// A request that reaches the socket straight from this machine or from a
// tailnet peer, with nothing in between, is let through as before: that is the
// terminal client, and a laptop on the tailnet. Anything relayed must carry the
// cookie the login form hands out, and every tunnel is a relay connecting from
// 127.0.0.1. A relay is told apart by the forwarding headers it adds (tailscale
// serve and funnel set X-Forwarded-Host, cloudflared Cf-Connecting-Ip), which a
// client on the far side can add to but not strip.
//
// One item can be handed to somebody else: the link to its page carries a
// signature of its app+id, which opens that page and the media it plays, and
// nothing else.
//
// The cookie and the signatures are both keyed off the password, so changing it
// signs every browser out and kills every link given away.
type webAuth struct {
	key    []byte
	digest [sha256.Size]byte

	// Paths anyone may fetch (what installs the page), and the ones a guest
	// holding a shared item's cookie may: the proxies its media plays through.
	public, media map[string]bool

	mu    sync.Mutex
	fails []time.Time
}

// webGate is nil when no password is set, which leaves the server as open as it
// always was.
var webGate *webAuth

const (
	webPasswordEnv = "TUI_WEB_PASSWORD"
	sessionCookie  = "tui_session"
	guestCookie    = "tui_guest"
	shareParam     = "share"
	sessionLife    = 365 * 24 * time.Hour
	guestLife      = 7 * 24 * time.Hour
	// Wrong passwords allowed, across every client, before the form stops
	// listening for a while. The owner is already signed in for a year, so
	// locking a guesser out locks nobody else out.
	loginFailMax    = 10
	loginFailWindow = 10 * time.Minute
)

func newWebAuthFromEnv() *webAuth {
	pw := os.Getenv(webPasswordEnv)
	if pw == "" {
		return nil
	}
	return newWebAuth(pw)
}

func newWebAuth(password string) *webAuth {
	mac := hmac.New(sha256.New, []byte(password))
	mac.Write([]byte("tui web auth"))
	a := &webAuth{
		key:    mac.Sum(nil),
		digest: sha256.Sum256([]byte(password)),
		public: map[string]bool{"/manifest.webmanifest": true, "/sw.js": true},
		media:  map[string]bool{"/img": true, "/dl": true, "/ytlen": true, "/redgif": true, biliPath: true},
	}
	for path := range iconSizes {
		a.public[path] = true
	}
	return a
}

type guestKey struct{}

func isGuest(ctx context.Context) bool {
	g, _ := ctx.Value(guestKey{}).(bool)
	return g
}

func (a *webAuth) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		switch {
		case r.URL.Path == "/login":
			a.login(w, r)
			return
		case a.public[r.URL.Path], a.owner(r):
		case r.URL.Path == "/item" && a.shared(r.URL.Query()):
			a.setCookie(w, r, guestCookie, guestLife)
			r = r.WithContext(context.WithValue(r.Context(), guestKey{}, true))
		case a.media[r.URL.Path] && a.validCookie(r, guestCookie):
		default:
			a.deny(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *webAuth) owner(r *http.Request) bool {
	return direct(r) || a.validCookie(r, sessionCookie)
}

var relayHeaders = []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip", "Forwarded", "Cf-Connecting-Ip"}

var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// direct is a request with no relay in front of it, from this machine or from a
// tailnet peer. A tailnet peer has to have arrived on this machine's own tailnet
// address, since 100.64/10 is also what carrier-grade NAT hands out on some
// networks.
func direct(r *http.Request) bool {
	for _, h := range relayHeaders {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	remote, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := remote.Addr().Unmap()
	if ip.IsLoopback() {
		return true
	}
	local, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if local == nil {
		return false
	}
	lp, err := netip.ParseAddrPort(local.String())
	if err != nil {
		return false
	}
	return inTailnet(ip) && inTailnet(lp.Addr().Unmap())
}

func inTailnet(ip netip.Addr) bool {
	return tailnetV4.Contains(ip) || tailnetV6.Contains(ip)
}

func (a *webAuth) sign(s string) string {
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

func (a *webAuth) verify(s, sig string) bool {
	return hmac.Equal([]byte(a.sign(s)), []byte(sig))
}

// itemLink is itemHref with the signature that lets somebody without the
// password open it. A nil gate has nothing to sign with, and nothing needs it.
func (a *webAuth) itemLink(app, id string) string {
	href := itemHref(app, id)
	if a == nil || href == "" {
		return href
	}
	return href + "&" + shareParam + "=" + a.sign("item\x00"+app+"\x00"+id)
}

func (a *webAuth) shared(q url.Values) bool {
	app, id, sig := q.Get("app"), q.Get("id"), q.Get(shareParam)
	return app != "" && id != "" && sig != "" && a.verify("item\x00"+app+"\x00"+id, sig)
}

// A cookie is its expiry and a signature over that expiry and its own name, so
// a guest's cookie can't be passed off as the owner's.
func (a *webAuth) setCookie(w http.ResponseWriter, r *http.Request, name string, life time.Duration) {
	exp := strconv.FormatInt(time.Now().Add(life).Unix(), 10)
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    exp + "." + a.sign(name+"\x00"+exp),
		Path:     "/",
		MaxAge:   int(life / time.Second),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *webAuth) validCookie(r *http.Request, name string) bool {
	c, err := r.Cookie(name)
	if err != nil {
		return false
	}
	exp, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !a.verify(name+"\x00"+exp, sig) {
		return false
	}
	t, err := strconv.ParseInt(exp, 10, 64)
	return err == nil && time.Now().Unix() < t
}

// deny answers a page load with the login form, and anything else (a fetch from
// a page whose cookie ran out) with a bare 401. Either way the status is 401 and
// not a redirect, so the service worker can tell it from a page worth caching.
func (a *webAuth) deny(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
		writeLogin(w, http.StatusUnauthorized, r.URL.RequestURI(), "")
		return
	}
	http.Error(w, "login required", http.StatusUnauthorized)
}

func (a *webAuth) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	switch r.Method {
	case http.MethodGet:
		if a.owner(r) {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		writeLogin(w, http.StatusOK, next, "")
	case http.MethodPost:
		if !a.allowAttempt() {
			writeLogin(w, http.StatusTooManyRequests, next, "Too many wrong passwords. Try again in a few minutes.")
			return
		}
		given := sha256.Sum256([]byte(r.FormValue("password")))
		if subtle.ConstantTimeCompare(given[:], a.digest[:]) != 1 {
			a.recordFail()
			writeLogin(w, http.StatusUnauthorized, next, "Wrong password.")
			return
		}
		a.setCookie(w, r, sessionCookie, sessionLife)
		http.Redirect(w, r, next, http.StatusSeeOther)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *webAuth) allowAttempt() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cut := time.Now().Add(-loginFailWindow)
	kept := a.fails[:0]
	for _, t := range a.fails {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	a.fails = kept
	return len(a.fails) < loginFailMax
}

func (a *webAuth) recordFail() {
	a.mu.Lock()
	a.fails = append(a.fails, time.Now())
	a.mu.Unlock()
}

// safeNext keeps the post-login redirect on this server: a path, and not a
// scheme-relative "//host" or "/\host" that a browser would read as another site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

var loginTmpl = template.Must(template.New("login").Parse(`<!doctype html>
<html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>tui</title>
<style>
body{margin:0;background:#111318;color:#e6e9ee;font:16px/1.55 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,sans-serif}
form{max-width:360px;margin:18vh auto 0;padding:0 16px;display:flex;flex-direction:column;gap:12px}
input,button{font:inherit;padding:10px 12px;border-radius:8px;border:1px solid #2a2f3a;background:#1a1d24;color:inherit}
button{background:#4a9eff;border-color:#4a9eff;color:#fff;font-weight:700}
.err{color:#ff6b6b;margin:0}
@media (prefers-color-scheme:light){body{background:#f5f6f8;color:#1a1d24}input{background:#fff;border-color:#d5d9e0}}
</style></head><body>
<form method="post" action="/login">
<input type="hidden" name="next" value="{{.Next}}">
<input type="text" name="username" value="tui" autocomplete="username" hidden>
<input type="password" name="password" placeholder="password" autocomplete="current-password" autofocus required>
{{if .Err}}<p class="err">{{.Err}}</p>{{end}}
<button type="submit">sign in</button>
</form></body></html>`))

func writeLogin(w http.ResponseWriter, status int, next, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = loginTmpl.Execute(w, struct{ Next, Err string }{next, msg})
}
