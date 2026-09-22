// The service worker behind the installed page. It exists for one thing: the
// saved list has to be readable on a train, where the server on the desk at
// home is not in reach. Everything else here follows from that.
//
// Nothing is precached at install time. What the saved list needs is the saved
// list, and that is a page the server renders out of its own store, so the
// worker keeps the copy it last served rather than a shell it would have to
// fill from an API the page does not have. A visit while online refreshes that
// copy; a visit without a network gets the last one.
//
// Read marks, saving, tagging and briefings all want the server. Offline they
// fail as they always have, and the page says so.
const VERSION = 'v1';
const PAGES = 'tui-pages-' + VERSION;
const MEDIA = 'tui-media-' + VERSION;
const FONTS = 'tui-fonts-' + VERSION;
const KEEP = [PAGES, MEDIA, FONTS];

// A saved list full of phone screenshots would fill a cache quota by itself, so
// the stills are kept newest-first up to a ceiling. Cache keys come back in
// insertion order, which makes the oldest the ones to drop.
const MEDIA_MAX = 300;

// How long a page waits for the server before the cached copy is served
// instead. A tailnet that is up answers in tens of milliseconds; one that is
// half up — the phone has bars, the laptop at home is asleep — can leave a
// request hanging until the browser gives up on it, which is a minute of
// looking at nothing when the answer was on the device the whole time. The
// request is left running: whatever it brings back still refreshes the cache
// for the next visit.
const PAGE_WAIT = 6000;

self.addEventListener('install', function(e){ self.skipWaiting(); });

self.addEventListener('activate', function(e){
  e.waitUntil(caches.keys().then(function(names){
    return Promise.all(names.map(function(n){
      return KEEP.indexOf(n) < 0 ? caches.delete(n) : null;
    }));
  }).then(function(){ return self.clients.claim(); }));
});

// ?json=1 is the same list for scripts rather than for reading, and caching it
// under the page's own URL would hand a reader JSON.
function isSavedPage(url){
  return url.pathname === '/' && url.searchParams.get('saved') === '1' &&
    url.searchParams.get('json') !== '1';
}

function isItemPage(url){
  return url.pathname === '/item' && url.searchParams.get('json') !== '1';
}

// whole fetches req and reads the body to the end before anyone sees it.
//
// The reading-to-the-end is the point. fetch resolves as soon as the headers
// land, so handing that response straight to the page hands over a stream that
// is still arriving — and a tailnet over cellular drops those. The page paints
// half a list and then the browser replaces it with a connection-lost screen,
// which is the one thing a cached copy exists to prevent, and by then it is too
// late to serve one. Buffering costs the saved list its progressive render and
// turns a dropped connection back into something the cache can answer.
//
// A response that is not ok is treated as no response at all: with a reverse
// proxy in front (tailscale serve), a server that is down answers 502, and a
// reader should get the list rather than the proxy's apology.
//
// The new response carries the content type and nothing else: the body has
// already been decoded, so the original's encoding and length headers would
// describe bytes that no longer exist.
function whole(req, keep){
  return fetch(req).then(function(res){
    if (!res || !res.ok) throw new Error('unusable: ' + (res && res.status));
    return res.arrayBuffer().then(function(buf){
      var out = new Response(buf, {
        status: 200,
        headers: {'Content-Type': res.headers.get('Content-Type') || 'text/html; charset=utf-8'},
      });
      if (keep) {
        var copy = out.clone();
        caches.open(PAGES).then(function(c){ return c.put(req.url, copy); }).catch(function(){});
      }
      return out;
    });
  });
}

// The server first, the cached copy if it is slow or gone. Keeping the server
// ahead matters: a saved item can be untagged or dropped from another device,
// and a cached page that outranked it would be a bug rather than a feature.
function pageFirst(req){
  return caches.match(req.url).then(function(hit){
    var net = whole(req, true);
    if (!hit) {
      return net.catch(function(){
        return savedFallback().then(function(alt){ return alt || offlinePage(); });
      });
    }
    var patience = new Promise(function(resolve){ setTimeout(function(){ resolve(hit); }, PAGE_WAIT); });
    return Promise.race([net, patience]).catch(function(){ return hit; });
  });
}

// Any saved page that was cached, for the case where the exact URL asked for
// was not: a tag filter or a chip narrows the list, and arriving offline on one
// of those is better answered with the whole list than with an apology.
function savedFallback(){
  return caches.open(PAGES).then(function(c){
    return c.keys().then(function(reqs){
      for (var i = 0; i < reqs.length; i++){
        if (isSavedPage(new URL(reqs[i].url))) return c.match(reqs[i]);
      }
      return null;
    });
  });
}

function offlinePage(){
  var body = '<!doctype html><meta charset="utf-8">' +
    '<meta name="viewport" content="width=device-width, initial-scale=1">' +
    '<title>offline</title>' +
    '<body style="margin:0;background:#111318;color:#e6e9ee;font:16px/1.55 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,sans-serif">' +
    '<div style="max-width:640px;margin:0 auto;padding:24px 16px">' +
    '<h1 style="font-size:20px">offline</h1>' +
    '<p style="color:#949ba8">The feed lives on the server, which is not in reach right now. ' +
    'The saved list is readable offline once it has been opened at least once while connected.</p>' +
    '<p><a style="color:#4a9eff" href="/?saved=1">saved</a></p></div>';
  return new Response(body, {status: 503, headers: {'Content-Type': 'text/html; charset=utf-8'}});
}

function cacheFirst(req, name, cap){
  return caches.open(name).then(function(c){
    return c.match(req).then(function(hit){
      if (hit) return hit;
      return fetch(req).then(function(res){
        // An opaque response (a cross-origin still fetched no-cors) has no status
        // to check; it is stored anyway, since an <img> can draw it either way.
        if (res && (res.ok || res.type === 'opaque')) {
          c.put(req, res.clone());
          if (cap) trim(c, cap);
        }
        return res;
      });
    });
  });
}

function trim(cache, cap){
  cache.keys().then(function(keys){
    for (var i = 0; i < keys.length - cap; i++) cache.delete(keys[i]);
  });
}

// A promise handed to respondWith that rejects is a failed request: the browser
// draws its own connection-lost page and the reader is told the network broke
// when what actually broke was a cache. Storage can be denied outright (a
// private window, a browser set to block website data) or run out of room, so
// every answer below goes through here and ends at the network rather than at
// an error of our own making.
function guard(p, req){
  return p.catch(function(){ return fetch(req); });
}

self.addEventListener('fetch', function(e){
  var req = e.request;
  if (req.method !== 'GET') return;
  var url;
  try { url = new URL(req.url); } catch(err){ return; }

  if (url.origin === self.location.origin){
    if (isSavedPage(url) || isItemPage(url)){ e.respondWith(guard(pageFirst(req), req)); return; }
    // The feed, the skipped pile, the blocked list: all of them are a view of a
    // backlog that only the server holds, and a cached copy of one would be a
    // list of things you cannot mark read. Offline they hand over the saved
    // list instead, which is the part of the page that reads without a server.
    if (req.mode === 'navigate'){
      e.respondWith(whole(req, false).catch(function(){
        return savedFallback()
          .catch(function(){ return null; })
          .then(function(hit){ return hit || offlinePage(); });
      }));
      return;
    }
    if (url.pathname === '/manifest.webmanifest' || url.pathname.indexOf('/icon-') === 0 ||
        url.pathname === '/apple-touch-icon.png'){
      e.respondWith(guard(cacheFirst(req, PAGES, 0), req));
      return;
    }
    if (url.pathname === '/img'){ e.respondWith(guard(cacheFirst(req, MEDIA, MEDIA_MAX), req)); return; }
    return; // /status, /save, /summarize and the rest want the server or nothing
  }

  if (url.host === 'fonts.googleapis.com' || url.host === 'fonts.gstatic.com'){
    e.respondWith(guard(cacheFirst(req, FONTS, 0), req));
    return;
  }
  // Stills on a card come straight from the source's CDN. Video does not: a
  // clip is tens of megabytes and the cache is not the place for it.
  if (req.destination === 'image'){ e.respondWith(guard(cacheFirst(req, MEDIA, MEDIA_MAX), req)); return; }
});

// What is actually readable with the server gone: pages held, stills held. The
// page shows this, because "it will work offline" is a promise nobody can check
// by looking at a page that is working online.
function held(){
  return Promise.all([caches.open(PAGES), caches.open(MEDIA)]).then(function(cs){
    return Promise.all([cs[0].keys(), cs[1].keys()]);
  }).then(function(keys){
    var saved = 0, items = 0;
    keys[0].forEach(function(r){
      var u = new URL(r.url);
      if (isSavedPage(u)) saved++;
      else if (isItemPage(u)) items++;
    });
    return {version: VERSION, saved: saved, items: items, media: keys[1].length};
  }).catch(function(err){
    return {version: VERSION, error: String(err && err.message ? err.message : err)};
  });
}

function reply(e, data){
  if (e.ports && e.ports[0]) e.ports[0].postMessage(data);
}

// What the saved page asks for once it is on screen and the server is in reach:
// its own URL, each item's own page, and the stills its cards would draw if the
// image button were tapped. Priming is the difference between a saved list you
// can read offline and one you can only see the titles of — a card loads its
// images on a tap, so a worker watching traffic would never see them.
self.addEventListener('message', function(e){
  var msg = e.data || {};
  if (msg.type === 'status'){
    e.waitUntil(held().then(function(d){ reply(e, d); }));
    return;
  }
  if (msg.type !== 'prime') return;
  var pages = msg.pages || [], media = msg.media || [];
  e.waitUntil(Promise.all([
    caches.open(PAGES).then(function(c){
      return Promise.all(pages.map(function(u){
        return fetch(u, {credentials: 'same-origin'})
          .then(function(res){ if (res && res.ok) return c.put(u, res); })
          .catch(function(){});
      }));
    }),
    caches.open(MEDIA).then(function(c){
      return Promise.all(media.map(function(u){
        return c.match(u).then(function(hit){
          if (hit) return;
          // no-cors so a CDN that sends no CORS headers still lands in the
          // cache; the result is opaque, which is all an <img> needs.
          return fetch(u, {mode: 'no-cors'})
            .then(function(res){ if (res) return c.put(u, res); })
            .catch(function(){});
        });
      })).then(function(){ trim(c, MEDIA_MAX); });
    }),
  ]).then(function(){ return held(); }).then(function(d){ reply(e, d); }));
});
