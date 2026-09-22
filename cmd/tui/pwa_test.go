package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManifestNamesTheIcons(t *testing.T) {
	rec := httptest.NewRecorder()
	handleManifest(rec, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/manifest+json" {
		t.Errorf("content type = %q", got)
	}
	var m struct {
		StartURL string `json:"start_url"`
		Display  string `json:"display"`
		Icons    []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if m.StartURL != "/" || m.Display != "standalone" {
		t.Errorf("start %q display %q", m.StartURL, m.Display)
	}
	if len(m.Icons) == 0 {
		t.Fatal("no icons")
	}
	for _, ic := range m.Icons {
		if _, ok := iconSizes[ic.Src]; !ok {
			t.Errorf("manifest names %q, which is not served", ic.Src)
		}
	}
}

func TestServiceWorkerIsServedAtTheRoot(t *testing.T) {
	rec := httptest.NewRecorder()
	handleServiceWorker(rec, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Errorf("content type = %q", got)
	}
	// A worker the browser is allowed to cache is a caching policy that cannot
	// be replaced, so the header matters as much as the body.
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("cache-control = %q", got)
	}
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("scope = %q", got)
	}
	if !strings.Contains(rec.Body.String(), "addEventListener('fetch'") {
		t.Error("worker does not answer fetches")
	}
}

func TestIconsRenderAtEverySizeAsked(t *testing.T) {
	for path, size := range iconSizes {
		rec := httptest.NewRecorder()
		handleIcon(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "image/png" {
			t.Errorf("%s: content type = %q", path, got)
		}
		img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Errorf("%s: %dx%d, want %d square", path, b.Dx(), b.Dy(), size)
		}
	}
}

func TestIconOffTheListIs404(t *testing.T) {
	rec := httptest.NewRecorder()
	handleIcon(rec, httptest.NewRequest(http.MethodGet, "/icon-64.png", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestPageIsInstallableAndRegistersTheWorker(t *testing.T) {
	page := renderPage(t, nil, []string{"reddit"}, nil, "", "")
	for _, want := range []string{
		`<link rel="manifest" href="/manifest.webmanifest">`,
		`<link rel="apple-touch-icon" href="/apple-touch-icon.png">`,
		`navigator.serviceWorker.register('/sw.js')`,
		`id="offline"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %s", want)
		}
	}
}

func TestServeURLFindsTheHTTPSAddressForThisPort(t *testing.T) {
	status := []byte(`{
      "TCP": {"443": {"HTTPS": true}},
      "Web": {"san.bonobo-firefighter.ts.net:443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:8080"}}}}
    }`)
	if got := serveURLFor(status, "8080"); got != "https://san.bonobo-firefighter.ts.net/" {
		t.Errorf("serveURLFor = %q", got)
	}
	// A serve that fronts something else is not this server's address.
	if got := serveURLFor(status, "9000"); got != "" {
		t.Errorf("serveURLFor(other port) = %q, want blank", got)
	}
	if got := serveURLFor([]byte(`{}`), "8080"); got != "" {
		t.Errorf("serveURLFor(nothing served) = %q, want blank", got)
	}
	if got := serveURLFor([]byte("not json"), "8080"); got != "" {
		t.Errorf("serveURLFor(garbage) = %q, want blank", got)
	}
}
