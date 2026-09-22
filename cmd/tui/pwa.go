package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/http"
	"strconv"
	"sync"
)

// What makes the web view installable, and what makes the saved list readable
// with no server in reach: a manifest, an icon in the sizes a home screen asks
// for, and the worker in sw.js that keeps the last saved page it served.
//
// The icon is drawn here rather than checked in as a file. It is a few
// rectangles, the binary already ships every byte of the page it belongs to,
// and a PNG in the repo is a thing that drifts from the colors beside it.

//go:embed sw.js
var swSrc string

const manifestJSON = `{
  "name": "tui",
  "short_name": "tui",
  "description": "Every timeline in one backlog, with the saved list readable offline.",
  "start_url": "/",
  "scope": "/",
  "display": "standalone",
  "background_color": "#111318",
  "theme_color": "#111318",
  "icons": [
    {"src": "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
    {"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
    {"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"}
  ]
}
`

func handleManifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(manifestJSON))
}

// handleServiceWorker serves the worker. no-cache because a browser that holds
// on to an old worker holds on to an old caching policy, and the usual answer
// to a bad one is to ship a new worker.
func handleServiceWorker(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// Without this a worker served from /sw.js could only ever control /sw.js's
	// own directory, which here is the whole site anyway — but stating it keeps
	// the scope true if the page is ever served from below the root.
	w.Header().Set("Service-Worker-Allowed", "/")
	_, _ = w.Write([]byte(swSrc))
}

// iconSizes maps each icon route to the square it draws. 180 is what iOS asks
// for by name; the other two are what a manifest names.
var iconSizes = map[string]int{
	"/icon-192.png":         192,
	"/icon-512.png":         512,
	"/apple-touch-icon.png": 180,
}

var (
	iconMu   sync.Mutex
	iconPNGs = map[int][]byte{}
)

func handleIcon(w http.ResponseWriter, r *http.Request) {
	size, ok := iconSizes[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	iconMu.Lock()
	body, drawn := iconPNGs[size]
	if !drawn {
		body = iconPNG(size)
		iconPNGs[size] = body
	}
	iconMu.Unlock()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// iconPNG draws the home-screen icon: the page's own dark tile, a sheet on it,
// and the lines of a story. Everything sits inside the middle 60% so a launcher
// that crops the icon to a circle (a maskable icon's safe zone) crops nothing.
//
// Drawn at four times the size and averaged down, which is what keeps the
// rounded corners from looking like stairs.
func iconPNG(size int) []byte {
	const scale = 4
	big := image.NewRGBA(image.Rect(0, 0, size*scale, size*scale))
	n := float64(size * scale)

	bg := color.RGBA{0x11, 0x13, 0x18, 0xff}
	sheet := color.RGBA{0xe6, 0xe9, 0xee, 0xff}
	accent := color.RGBA{0x4a, 0x9e, 0xff, 0xff}
	line := color.RGBA{0x94, 0x9b, 0xa8, 0xff}

	draw.Draw(big, big.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	fillRound(big, .20*n, .18*n, .80*n, .82*n, .08*n, sheet)
	fillRound(big, .27*n, .26*n, .73*n, .37*n, .02*n, accent)
	fillRound(big, .27*n, .45*n, .73*n, .52*n, .02*n, line)
	fillRound(big, .27*n, .58*n, .73*n, .65*n, .02*n, line)
	fillRound(big, .27*n, .71*n, .58*n, .78*n, .02*n, line)

	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b uint32
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					c := big.RGBAAt(x*scale+dx, y*scale+dy)
					r, g, b = r+uint32(c.R), g+uint32(c.G), b+uint32(c.B)
				}
			}
			d := uint32(scale * scale)
			out.SetRGBA(x, y, color.RGBA{uint8(r / d), uint8(g / d), uint8(b / d), 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil
	}
	return buf.Bytes()
}

// fillRound paints the rectangle x0,y0..x1,y1 with corners of radius rad.
func fillRound(img *image.RGBA, x0, y0, x1, y1, rad float64, c color.RGBA) {
	for y := int(y0); y < int(y1); y++ {
		for x := int(x0); x < int(x1); x++ {
			fx, fy := float64(x)+.5, float64(y)+.5
			cx := math.Min(math.Max(fx, x0+rad), x1-rad)
			cy := math.Min(math.Max(fy, y0+rad), y1-rad)
			if math.Hypot(fx-cx, fy-cy) > rad {
				continue
			}
			img.SetRGBA(x, y, c)
		}
	}
}
