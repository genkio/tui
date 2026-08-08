package main

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/genkio/tui/core"
)

// renderPage builds the mobile-friendly all-timeline page. Styling is embedded
// (keeps the release a single self-contained binary) and responsive: cards
// stack full-width with tap-sized targets, and the palette follows the phone's
// light/dark setting.
func renderPage(items []core.Item, apps, failed []string, now time.Time, asc bool, xTab, warn string) string {
	var b strings.Builder

	xAuth := "false"
	for _, a := range apps {
		if a == "x" {
			xAuth = "true"
			break
		}
	}

	var meta []string
	if len(apps) > 0 {
		meta = append(meta, fmt.Sprintf("%d unread", len(items)))
	}
	meta = append(meta, "updated "+now.Local().Format("15:04"))

	b.WriteString(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>all — tui</title>
<link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'><text y='13' font-size='12'>🗞️</text></svg>">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;600;700&display=swap" rel="stylesheet">
<style>
:root{
  --bg:#111318; --card:#181c23; --line:#262b34; --fg:#e6e9ee;
  --muted:#949ba8; --accent:#4a9eff; --ok:#3fb950; --bad:#f85149;
}
@media (prefers-color-scheme: light){
  :root{ --bg:#f6f7f9; --card:#ffffff; --line:#e3e6eb; --fg:#1b1f26; --muted:#6b7280; --accent:#1a73e8; --bad:#d64545; }
}
*{box-sizing:border-box}
html,body{margin:0;padding:0}
body{
  background:var(--bg); color:var(--fg);
  font:16px/1.55 "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  -webkit-text-size-adjust:100%;
}
.wrap{max-width:640px;margin:0 auto;padding:14px 14px 40px}
header{
  position:sticky;top:0;z-index:10;background:var(--bg);
  display:flex;align-items:center;gap:10px;padding:12px 2px 10px;
  border-bottom:1px solid var(--line);margin-bottom:12px;
}
h1{font-size:20px;margin:0;font-weight:700;letter-spacing:-.02em}
.sub{color:var(--muted);font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.sortbar{display:flex;align-items:center;gap:12px;font-size:13px;margin:-4px 0 12px;color:var(--muted)}
.sortbar .slabel{opacity:.7}
.sortbar a{color:var(--muted);text-decoration:none}
.sortbar .on{font-weight:700;color:var(--fg)}
.warn{background:rgba(214,69,69,.12);border:1px solid rgba(214,69,69,.5);color:var(--fg);border-radius:10px;padding:10px 12px;margin:-4px 0 12px;font-size:14px}
.health{margin-left:auto;display:flex;align-items:center;gap:12px;font-size:13px;color:var(--muted);white-space:nowrap}
.svc{display:inline-flex;align-items:center;gap:5px}
.hdot{width:8px;height:8px;border-radius:50%;flex:none}
.hdot.ok{background:var(--ok)}
.hdot.bad{background:var(--bad)}
.card{
  background:var(--card);border:1px solid var(--line);border-radius:14px;
  padding:14px 16px;margin-bottom:12px;
}
.ctitle{font-size:17px;line-height:1.35;font-weight:650;color:var(--fg)}
.meta{display:flex;align-items:center;gap:8px;margin-top:8px;font-size:13px;color:var(--muted);flex-wrap:wrap}
.chip{font-weight:700;padding:1px 8px;border-radius:999px;font-size:12px;color:#fff}
.src{font-weight:600;color:var(--fg)}
.dot{opacity:.5}
.body{margin:8px 0 4px;color:var(--fg);white-space:pre-wrap;word-break:break-word;font-size:15px}
.body .link{color:var(--accent);text-decoration:underline;word-break:break-all}
.card.read{opacity:.45}
.footer{display:flex;align-items:center;gap:18px;margin-top:12px}
.markall{
  display:block;width:100%;margin:20px 0 0;padding:14px;
  background:var(--card);border:1px dashed var(--line);color:var(--accent);
  border-radius:14px;font-size:15px;font-weight:700;cursor:pointer;min-height:48px;
}
.markall:disabled{opacity:.5;cursor:default}
.open{color:var(--accent);text-decoration:none;font-weight:600;font-size:14px;min-height:40px;display:inline-flex;align-items:center;gap:4px}
.expand{background:none;border:none;color:var(--muted);font-size:14px;cursor:pointer;padding:0;min-height:40px}
.hid{display:none}
.empty{color:var(--muted);font-style:italic}
.dlg{border:1px solid var(--line);background:var(--card);color:var(--fg);border-radius:16px;padding:20px;max-width:360px;width:calc(100vw - 48px)}
.dlg::backdrop{background:rgba(0,0,0,.55)}
.dlg-title{margin:0 0 6px;font-size:19px;font-weight:700}
.dlg-hint{margin:0 0 16px;color:var(--muted);font-size:15px}
.dlg-actions{display:flex;gap:10px}
.dlg-actions button{flex:1;border-radius:12px;padding:12px 14px;font-size:15px;font-weight:600;min-height:44px;cursor:pointer}
.dlg-go{border:none;background:var(--accent);color:#fff}
.dlg-no{border:1px solid var(--line);background:transparent;color:var(--fg)}
.note{padding:16px;color:var(--muted);border:1px dashed var(--line);border-radius:12px;font-size:15px}
.note a{color:var(--accent);font-weight:600;text-decoration:none}
.note.offer{margin-top:10px}
.toast{
  position:fixed;left:50%;bottom:18px;transform:translateX(-50%);
  background:var(--fg);color:var(--bg);font-size:14px;font-weight:600;
  padding:8px 16px;border-radius:999px;opacity:0;pointer-events:none;transition:opacity .2s;
}
</style>
</head><body>
<div id="state" data-xauth="` + xAuth + `" data-xtab="` + xTab + `" hidden></div>
<div class="wrap">
<header>
  <div class="sub" id="sub">` + escape(strings.Join(meta, " · ")) + `</div>
` + renderHealth(apps, failed) + `</header>
` + renderSortbar(asc) + `
` + renderWarn(warn) + `
`)

	if len(items) == 0 {
		if len(apps) == 0 {
			b.WriteString(`<div class="note">No reader app is logged in. Run <code>tui &lt;app&gt; --auth</code> on the host, then refresh this page.</div>`)
		} else {
			b.WriteString(`<div class="note">Inbox zero across every timeline.</div>`)
			// With x authed on Following and nothing left to read, give a direct
			// way into For You right from the empty state.
			if xAuth == "true" && xTab == "following" {
				b.WriteString(`<div class="note offer">All caught up. <a href="/?x=foryou">Continue with For You from x →</a></div>`)
			}
		}
	}
	for _, it := range items {
		b.WriteString(renderCard(it))
	}
	if len(items) > 0 {
		// Scroll-to-read can never reach the last few cards (they never clear
		// the top), so offer a one-tap mark-all-read button at the end.
		b.WriteString(`<button id="markAll" class="markall" type="button">✓ mark all read</button>`)
	}
	b.WriteString(`<div id="toast" class="toast"></div>
<dialog id="forYou" class="dlg">
  <div class="dlg-body">
    <p class="dlg-title">All read.</p>
    <p class="dlg-hint">Everything's clear. Want to keep going on x's For You timeline?</p>
  </div>
  <div class="dlg-actions">
    <button id="forYouGo" class="dlg-go" type="button">Continue on For You</button>
    <button id="forYouNo" class="dlg-no" type="button">Not now</button>
  </div>
</dialog>
<script>
// Reading = scrolling: the moment a card is fully off-screen (its bottom edge
// clears the top of the viewport) it is marked read and muted in place — it
// stays in the list, greyed, so scrolling back up shows what you've read.
// Marks are batched per app so a fast scroll is one --mark-read call per app.
var pending = {};   // app -> ids that scrolled off, awaiting a flush
var flushTimer = null;
function flushPending(){
  flushTimer = null;
  var apps = Object.keys(pending);
  var dirty = apps.some(function(a){ return pending[a].length > 0; });
  if(!dirty) return;
  apps.forEach(function(app){
    var ids = pending[app]; if(!ids.length) return;
    pending[app] = [];
    var fd = new FormData(); fd.append('app', app); fd.append('json','1');
    ids.forEach(function(id){ fd.append('id', id); });
    fetch('/mark',{method:'POST',body:fd})
      .then(function(res){ return res.json(); })
      .then(function(d){ if(!d.ok) throw 0; decCount(ids.length); })
      .catch(function(){ toast('could not save some reads'); });
  });
}
function decCount(n){
  var s = document.getElementById('sub'); if(!s) return;
  var m = s.textContent.match(/^(\d+)/);
  var c = (m ? parseInt(m[1],10) : 0) - n; if(c < 0) c = 0;
  s.textContent = s.textContent.replace(/^\d+/, c);
  if(c === 0) maybeOfferForYou();   // whole list read
}
function toast(m){ var t=document.getElementById('toast'); t.textContent=m; t.style.opacity='1'; setTimeout(function(){t.style.opacity='0'},1200); }
var obs = new IntersectionObserver(function(entries){
  var dirty = false;
  entries.forEach(function(en){
    if(en.isIntersecting) return;                 // still visible
    var el = en.target;
    if(el.getBoundingClientRect().bottom > 0) return; // below, not yet scrolled past
    if(el.classList.contains('read')) return;     // already muted once
    var app = el.dataset.app, id = el.dataset.id;
    if(!app || !id) return;
    el.classList.add('read');                     // mute in place; keep in the list
    (pending[app] = pending[app] || []).push(id);
    dirty = true;
  });
  if(dirty && !flushTimer) flushTimer = setTimeout(flushPending, 400);
},{threshold:0});
document.querySelectorAll('article.card').forEach(function(c){ obs.observe(c); });

// Expand/collapse reveals the full post content.
document.querySelectorAll('.expand').forEach(function(btn){
  btn.addEventListener('click', function(){
    var card = btn.closest('article.card');
    var p = card.querySelector('.preview'), f = card.querySelector('.full');
    p.classList.toggle('hid'); f.classList.toggle('hid');
    btn.textContent = p.classList.contains('hid') ? 'less' : 'more';
  });
});
// Mark everything read with one tap (scroll can't reach the last cards).
var markAllBtn = document.getElementById('markAll');
if(markAllBtn) markAllBtn.addEventListener('click', function(){
  var groups = {};
  document.querySelectorAll('article.card:not(.read)').forEach(function(card){
    var app = card.dataset.app, id = card.dataset.id;
    if(!app || !id) return;
    (groups[app] = groups[app] || []).push(id);
    card.classList.add('read');
  });
  var n = 0;
  Object.keys(groups).forEach(function(app){
    var ids = groups[app]; n += ids.length;
    var fd = new FormData(); fd.append('app', app); fd.append('json','1');
    ids.forEach(function(id){ fd.append('id', id); });
    fetch('/mark',{method:'POST',body:fd})
      .then(function(res){ return res.json(); })
      .then(function(d){ if(!d.ok) throw 0; })
      .catch(function(){ toast('could not save all reads'); });
  });
  decCount(n);
  markAllBtn.textContent = '✓ all read';
  markAllBtn.disabled = true;
});

// x feed state (from the server): whether x is authed and which tab is live.
var st = document.getElementById('state');
var X_AUTHED = st ? st.getAttribute('data-xauth') === 'true' : false;
var X_TAB = st ? st.getAttribute('data-xtab') : 'following';
var forYouPrompted = false;
// The ?x=foryou hand-off is one-shot: strip the param so any later reload
// returns to the normal Following default.
if(location.search.indexOf('x=foryou') !== -1){ history.replaceState(null, '', location.pathname); }
// Once the whole list is read, offer to keep going on x For You.
function maybeOfferForYou(){
  if(forYouPrompted || !X_AUTHED || X_TAB !== 'following') return;
  forYouPrompted = true;
  document.getElementById('forYou').showModal();
}
document.getElementById('forYouGo').addEventListener('click', function(){ location.href = '/?x=foryou'; });
document.getElementById('forYouNo').addEventListener('click', function(){ document.getElementById('forYou').close(); });
</script>
</div></body></html>`)
	return b.String()
}

// renderHealth draws one labeled dot per logged-in service in the header:
// green when this page load fetched it fine, red when it failed. It replaces
// the refresh button (reload the page to refresh), so the health of every
// source is visible on each load instead of a source dying silently.
func renderHealth(apps, failed []string) string {
	if len(apps) == 0 {
		return ""
	}
	bad := map[string]bool{}
	for _, f := range failed {
		bad[f] = true
	}
	var b strings.Builder
	b.WriteString(`<div class="health">`)
	for _, a := range apps {
		label := appLabels[a]
		if label == "" {
			label = a
		}
		cls, title := "ok", a+": live"
		if bad[a] {
			cls, title = "bad", a+": failed to load"
		}
		b.WriteString(`<span class="svc" title="` + escape(title) + `"><span class="hdot ` + cls + `"></span>` + escape(label) + `</span>`)
	}
	b.WriteString(`</div>
`)
	return b.String()
}

// renderSortbar draws the oldest/newest order toggle; the active choice is
// rendered as plain text (not a link).
func renderSortbar(asc bool) string {
	var oldest, newest string
	if asc {
		oldest = `<span class="sort on">oldest</span>`
		newest = `<a class="sort" href="?order=desc">newest</a>`
	} else {
		oldest = `<a class="sort" href="?order=asc">oldest</a>`
		newest = `<span class="sort on">newest</span>`
	}
	return `<div class="sortbar"><span class="slabel">sort</span>` + oldest + newest + `</div>`
}

// renderWarn renders a prominent banner for a fixable condition (e.g. an
// expired session), or nothing when there is nothing to warn about.
func renderWarn(warn string) string {
	if warn == "" {
		return ""
	}
	return `<div class="warn">⚠ ` + escape(warn) + `</div>`
}

// renderCard renders one item as a card.
func renderCard(it core.Item) string {
	chip := appLabels[it.App]
	if chip == "" {
		chip = it.App
	}
	color := appColors[it.App]
	if color == "" {
		color = "#4a9eff"
	}

	var b strings.Builder
	b.WriteString(`<article class="card" data-app="` + escape(it.App) + `" data-id="` + escape(it.ID) + `">`)
	b.WriteString(`<div class="meta">`)
	b.WriteString(`<span class="chip" style="background:` + color + `">` + escape(chip) + `</span>`)
	if it.Source != "" {
		b.WriteString(`<span class="src">` + escape(it.Source) + `</span>`)
	}
	if it.Author != "" && it.Author != it.Source {
		b.WriteString(`<span class="dot">·</span><span>` + escape(it.Author) + `</span>`)
	}
	if it.Age != "" {
		b.WriteString(`<span class="dot">·</span><span>` + escape(it.Age) + `</span>`)
	}
	b.WriteString(`</div>`)

	title := strings.TrimSpace(it.Title)
	body := strings.TrimSpace(it.Body)
	dup := body != "" && body == title // x carries the full text as both title and body

	// Two content panels: a clipped preview and a full version the footer's
	// expand toggle reveals.
	var preview, full strings.Builder
	if dup {
		preview.WriteString(`<div class="body">` + linkify(clip(body, 220)) + `</div>`)
		full.WriteString(`<div class="body">` + linkify(body) + `</div>`)
	} else {
		if title != "" {
			preview.WriteString(`<div class="ctitle">` + escape(title) + `</div>`)
			full.WriteString(`<div class="ctitle">` + escape(title) + `</div>`)
		}
		if body != "" {
			preview.WriteString(`<div class="body">` + linkify(clip(body, 220)) + `</div>`)
			full.WriteString(`<div class="body">` + linkify(body) + `</div>`)
		}
	}
	b.WriteString(`<div class="preview">` + preview.String() + `</div>`)
	b.WriteString(`<div class="full hid">` + full.String() + `</div>`)

	// Footer: open the original post (link icon, new tab) and expand/collapse.
	b.WriteString(`<div class="footer">`)
	if it.URL != "" {
		b.WriteString(`<a class="open" href="` + escape(it.URL) + `" target="_blank" rel="noopener" title="open original"><span>open</span></a>`)
	}
	if needsExpand(body, title) {
		b.WriteString(`<button class="expand" type="button">more</button>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`</article>`)
	return b.String()
}

// needsExpand reports whether anything is clipped, i.e. there is more to reveal.
func needsExpand(body, title string) bool {
	return len([]rune(body)) > 220 || len([]rune(title)) > 220
}

// clip truncates s to at most n runes, adding an ellipsis when cut.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// linkRe matches a URL plus any trailing punctuation (so "see http://x.com."
// renders the period as plain text, not part of the link).
var linkRe = regexp.MustCompile(`(https?://[^\s<>"']+)([.,;:!?)\]}"']*)`)

// linkify HTML-escapes text and turns embedded URLs into clickable links that
// open in a new tab; non-URL text is escaped as before.
func linkify(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range linkRe.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(escape(s[last:m[2]])) // text before the URL
		u := s[m[2]:m[3]]
		b.WriteString(`<a class="link" href="` + escape(u) + `" target="_blank" rel="noopener" title="` + escape(u) + `">` + escape(linkLabel(u)) + `</a>`)
		if m[4] >= 0 { // trailing punctuation kept as plain text
			b.WriteString(escape(s[m[4]:m[5]]))
		}
		last = m[1]
	}
	b.WriteString(escape(s[last:]))
	return b.String()
}

// linkLabel trims the scheme/www and shortens a URL for link text, keeping the
// full address in href and the title tooltip.
func linkLabel(u string) string {
	s := strings.TrimPrefix(u, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// writeJSONItems writes the merged feed as JSON, for API clients.
func writeJSONItems(w io.Writer, items []core.Item, failed []string) {
	type wireItem struct {
		App    string `json:"app"`
		ID     string `json:"id"`
		Title  string `json:"title"`
		Body   string `json:"body,omitempty"`
		Source string `json:"source,omitempty"`
		Author string `json:"author,omitempty"`
		URL    string `json:"url,omitempty"`
		Age    string `json:"age,omitempty"`
		TS     string `json:"ts,omitempty"`
	}
	out := make([]wireItem, 0, len(items))
	for _, it := range items {
		wi := wireItem{
			App:    it.App,
			ID:     it.ID,
			Title:  it.Title,
			Body:   it.Body,
			Source: it.Source,
			Author: it.Author,
			URL:    it.URL,
			Age:    it.Age,
		}
		if !it.At.IsZero() {
			wi.TS = it.At.UTC().Format(time.RFC3339)
		}
		out = append(out, wi)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":  out,
		"failed": failed,
	})
}
