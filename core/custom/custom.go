// Package custom runs custom plugins: one JavaScript file each, which fetches a
// page and parses it into feed items, the way an RSSHub route does. They need
// no login and no binary of their own, so a new source is a file dropped into
// the directory `tui serve --plugins` names rather than a plugin compiled in.
//
// A parser is CommonJS: it sets module.exports to an object with a fetch
// function returning an array of items, and optionally a label, color and
// description for its chip. It runs in an embedded engine with no filesystem or
// process access; what it gets is get(url, {encoding, headers}) for the page,
// load(html) for a cheerio-style $, and console.log for the server log.
package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
	"golang.org/x/net/html/charset"

	"github.com/genkio/tui/core"
)

// Meta is what a parser says about itself, for its chip.
type Meta struct {
	Label       string
	Color       string
	Description string
}

const (
	// A browser's, since the sites worth scraping this way tend to turn away
	// anything that says it is a script.
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	maxBody   = 32 << 20
)

var client = &http.Client{Timeout: time.Minute}

// Load evaluates a parser for its metadata. Nothing is fetched: fetch is only
// looked up, which is also how a file that is not a parser at all is caught.
func Load(path string) (Meta, error) {
	r, exp, err := open(context.Background(), path)
	if err != nil {
		return Meta{}, err
	}
	if _, ok := goja.AssertFunction(exp.Get("fetch")); !ok {
		return Meta{}, errors.New("module.exports has no fetch function")
	}
	return Meta{
		Label:       r.str(exp.Get("label")),
		Color:       r.str(exp.Get("color")),
		Description: r.str(exp.Get("description")),
	}, nil
}

// Fetch runs a parser's fetch and returns its items with App unset, for the
// caller to file under the plugin's name. A fetch declared async is waited on.
// ctx bounds the whole run: its requests and the script's own loops alike.
func Fetch(ctx context.Context, path string) ([]core.Wire, error) {
	r, exp, err := open(ctx, path)
	if err != nil {
		return nil, err
	}
	fn, ok := goja.AssertFunction(exp.Get("fetch"))
	if !ok {
		return nil, errors.New("module.exports has no fetch function")
	}
	v, err := fn(exp)
	if err != nil {
		return nil, r.fail(err)
	}
	if p, ok := v.Export().(*goja.Promise); ok {
		switch p.State() {
		case goja.PromiseStateFulfilled:
			v = p.Result()
		case goja.PromiseStateRejected:
			return nil, fmt.Errorf("fetch: %s", p.Result())
		default:
			return nil, errors.New("fetch: promise never settled")
		}
	}
	raw, ok := v.Export().([]any)
	if !ok {
		return nil, errors.New("fetch did not return an array")
	}
	out := make([]core.Wire, 0, len(raw))
	for i, x := range raw {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("item %d is not an object", i)
		}
		w, err := wireOf(m)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		out = append(out, w)
	}
	return out, nil
}

type runtime struct {
	ctx context.Context
	vm  *goja.Runtime
}

func open(ctx context.Context, path string) (*runtime, *goja.Object, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	prog, err := goja.Compile(filepath.Base(path), string(src), false)
	if err != nil {
		return nil, nil, err
	}
	r := &runtime{ctx: ctx, vm: goja.New()}
	r.vm.SetFieldNameMapper(goja.UncapFieldNameMapper())
	// The engine checks for an interrupt between instructions, so a parser stuck
	// in a loop is stopped by the same deadline as one stuck on a request.
	context.AfterFunc(ctx, func() { r.vm.Interrupt(ctx.Err()) })

	module := r.vm.NewObject()
	exports := r.vm.NewObject()
	module.Set("exports", exports)
	r.vm.Set("module", module)
	r.vm.Set("exports", exports)
	r.vm.Set("get", r.get)
	r.vm.Set("load", r.load)
	console := r.vm.NewObject()
	console.Set("log", r.log)
	r.vm.Set("console", console)
	if _, err := r.vm.RunProgram(prog); err != nil {
		return nil, nil, r.fail(err)
	}
	exp := module.Get("exports")
	if exp == nil || goja.IsUndefined(exp) || goja.IsNull(exp) {
		return nil, nil, errors.New("module.exports is empty")
	}
	return r, exp.ToObject(r.vm), nil
}

// fail reports a script's error the way the chip's tooltip wants it: one line,
// the deadline named as such rather than as an engine interrupt.
func (r *runtime) fail(err error) error {
	var intr *goja.InterruptedError
	if errors.As(err, &intr) {
		if cause, ok := intr.Value().(error); ok {
			return cause
		}
	}
	return errors.New(strings.TrimSpace(err.Error()))
}

func (r *runtime) str(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

type getOpts struct {
	Encoding string
	Headers  map[string]string
}

// get fetches url and returns its body as text. The encoding is read off the
// response (header, BOM, <meta charset>) unless the parser names one, for the
// sites that declare the wrong one or none at all.
func (r *runtime) get(url string, opts goja.Value) (string, error) {
	var o getOpts
	if opts != nil && !goja.IsUndefined(opts) && !goja.IsNull(opts) {
		if err := r.vm.ExportTo(opts, &o); err != nil {
			return "", fmt.Errorf("get: options: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range o.Headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("GET %s: %s", url, res.Status)
	}
	body := io.LimitReader(res.Body, maxBody)
	var rd io.Reader
	if o.Encoding != "" {
		enc, _ := charset.Lookup(o.Encoding)
		if enc == nil {
			return "", fmt.Errorf("get: unknown encoding %q", o.Encoding)
		}
		rd = enc.NewDecoder().Reader(body)
	} else if rd, err = charset.NewReader(body, res.Header.Get("Content-Type")); err != nil {
		return "", err
	}
	b, err := io.ReadAll(rd)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *runtime) log(call goja.FunctionCall) goja.Value {
	parts := make([]string, len(call.Arguments))
	for i, a := range call.Arguments {
		parts[i] = a.String()
	}
	fmt.Fprintln(os.Stderr, "tui serve: plugin: "+strings.Join(parts, " "))
	return goja.Undefined()
}

// load parses html and returns $, which takes a selector or an element that a
// selection handed out (toArray, each, map) and returns a selection.
func (r *runtime) load(src string) (goja.Value, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(src))
	if err != nil {
		return nil, err
	}
	return r.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		switch a := call.Argument(0).Export().(type) {
		case string:
			return r.vm.ToValue(r.wrap(doc.Find(a)))
		case *selection:
			return r.vm.ToValue(a)
		}
		panic(r.vm.NewTypeError("$ takes a selector or an element"))
	}), nil
}

// wireOf reads one item the parser returned. id is the only field required:
// it is what read state hangs on, so it has to be the same on every fetch.
// html is a body to flatten, with its images lifted onto the card, for the
// parser that has a post's markup and no reason to strip it itself.
func wireOf(m map[string]any) (core.Wire, error) {
	w := core.Wire{
		ID:     text(m["id"]),
		Title:  text(m["title"]),
		Body:   text(m["body"]),
		Source: text(m["source"]),
		Author: text(m["author"]),
		URL:    text(m["url"]),
		Type:   text(m["type"]),
		Video:  text(m["video"]),
		Poster: text(m["poster"]),
		Audio:  text(m["audio"]),
		TS:     stamp(m["ts"]),
	}
	if w.ID == "" {
		return core.Wire{}, errors.New("no id")
	}
	if imgs, ok := m["images"].([]any); ok {
		for _, x := range imgs {
			if u := core.ImageURL(text(x)); u != "" {
				w.Images = append(w.Images, u)
			}
		}
	}
	if h := text(m["html"]); h != "" {
		if w.Body == "" {
			w.Body = core.HTMLToText(h)
		}
		w.Images = append(w.Images, core.ImagesFromHTML(h)...)
	}
	return w, nil
}

func text(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64, int64:
		return fmt.Sprint(v)
	}
	return ""
}

// stamp takes ts as a Date, an RFC 3339 string, or epoch milliseconds, and
// drops anything else: an item with no time sinks rather than failing the fetch.
func stamp(v any) string {
	var t time.Time
	switch v := v.(type) {
	case time.Time:
		t = v
	case string:
		t, _ = time.Parse(time.RFC3339, strings.TrimSpace(v))
	case int64:
		t = time.UnixMilli(v)
	case float64:
		t = time.UnixMilli(int64(v))
	}
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
