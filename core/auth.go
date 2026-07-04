package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

// FindChromium returns the path to a Chromium-family browser to drive for login
// (Brave, Chrome, Chromium, Edge, Vivaldi, Arc), or an error naming what to
// install. Any Chromium browser speaks the DevTools protocol; WebKit (Safari)
// does not, so a Safari-only user sets credentials by hand instead.
func FindChromium() (string, error) {
	if runtime.GOOS == "darwin" {
		for _, app := range []string{"Brave Browser", "Google Chrome", "Chromium", "Microsoft Edge", "Vivaldi", "Arc"} {
			p := "/Applications/" + app + ".app/Contents/MacOS/" + app
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	} else {
		for _, name := range []string{"brave-browser", "brave", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "vivaldi-stable"} {
			if p, err := exec.LookPath(name); err == nil {
				return p, nil
			}
		}
	}
	return "", errors.New("no Chromium-family browser found (Brave, Chrome, Chromium, Edge, …); install one, or set credentials by hand in " + UserEnvPath())
}

// Session is the captured browser state a plugin reads its credentials from.
type Session struct {
	cookies []*network.Cookie
	ctx     context.Context
}

// Cookie returns the named cookie's value, optionally restricted to a cookie
// domain containing one of the given suffixes (x reads x.com with a twitter.com
// fallback).
func (s *Session) Cookie(name string, domains ...string) string {
	for _, c := range s.cookies {
		if c.Name != name {
			continue
		}
		if len(domains) == 0 {
			return c.Value
		}
		for _, d := range domains {
			if strings.Contains(c.Domain, d) {
				return c.Value
			}
		}
	}
	return ""
}

// CookieHeader joins every cookie whose domain contains suffix into a Cookie
// header value ("a=1; b=2"), for readers that authenticate with the whole
// session cookie.
func (s *Session) CookieHeader(suffix string) string {
	seen := map[string]bool{}
	var parts []string
	for _, c := range s.cookies {
		if !strings.Contains(c.Domain, suffix) || seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// Eval runs a JS expression in the logged-in page and returns its string result
// (slack reads its workspace token out of localStorage this way).
func (s *Session) Eval(js string) (string, error) {
	var out string
	err := chromedp.Run(s.ctx, chromedp.Evaluate(js, &out))
	return out, err
}

// RunAuth drives a browser login and saves the captured credentials. It opens
// loginURL in a detected Chromium browser under a dedicated persistent profile
// (so re-login is rare, and it never drives your real profile, which modern
// Chromium blocks CDP from anyway), waits for you to sign in and press Enter,
// then passes the session to capture and upserts the returned vars into
// UserEnvPath.
func RunAuth(ctx context.Context, loginURL string, capture func(*Session) (map[string]string, error)) error {
	browser, err := FindChromium()
	if err != nil {
		return err
	}
	profile := filepath.Join(userConfigDir(), "tui", "profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browser),
		chromedp.UserDataDir(profile),
		chromedp.Flag("headless", false),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	fmt.Printf("Opening %s in %s…\n", loginURL, filepath.Base(browser))
	if err := chromedp.Run(browserCtx, chromedp.Navigate(loginURL)); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}

	fmt.Println("\n  1. Log in to the site in the browser window that just opened.")
	fmt.Println("  2. Reach the logged-in page.")
	fmt.Println("  3. Come back here and press Enter to capture the session.")
	bufio.NewReader(os.Stdin).ReadString('\n')

	var cookies []*network.Cookie
	if err := chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		c, err := storage.GetCookies().Do(ctx)
		cookies = c
		return err
	})); err != nil {
		return fmt.Errorf("read cookies: %w", err)
	}

	vars, err := capture(&Session{cookies: cookies, ctx: browserCtx})
	if err != nil {
		return err
	}
	if len(vars) == 0 {
		return errors.New("nothing captured; were you fully logged in?")
	}
	if err := UpsertUserEnv(vars); err != nil {
		return err
	}
	fmt.Printf("\nSaved %d value(s) to %s.\n", len(vars), UserEnvPath())
	return nil
}
