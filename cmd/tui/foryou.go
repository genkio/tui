package main

import "github.com/genkio/tui/core"

// xForYouApp is x's other timeline as a source of its own. It is the same
// plugin, the same account and the same read state as x — only the tab differs,
// the algorithm's picks instead of the accounts you follow — but it is swept,
// cached, counted, summarized and cleared like every other source, which is the
// only way a firehose is any use: a briefing over the batch, and mark-all for
// the rest of it.
//
// It is a name in the cache rather than a flag on an item because everything
// here is keyed by app: one string, and the chips, the counts, the briefings,
// the item URLs and the bulk mark all work on it without knowing what it is.
const xForYouApp = "xforyou"

// pluginOf resolves a source to the plugin that serves it and the timeline to
// ask that plugin for. Every source is its own plugin except For You, which is
// the x plugin with the other --tab, so the subprocess, its .env and its
// read state are all x's.
func pluginOf(app string) (plugin, tab string) {
	if app == xForYouApp {
		return "x", "foryou"
	}
	return app, "following"
}

// twinApp is the source that shares this one's ids. x's two timelines are one
// stream of tweets seen through two windows: a post from someone you follow can
// surface in For You as well, and the same tweet id means the same tweet. The
// sweep uses this to keep one of them out of the backlog twice.
func twinApp(app string) string {
	switch app {
	case "x":
		return xForYouApp
	case xForYouApp:
		return "x"
	}
	return ""
}

// byPlugin groups sources by the plugin behind them, keeping the order they
// arrived in. One group is one subprocess's worth of work to do in turn: x's two
// timelines are one session upstream, and hitting it twice at once is asking one
// login to answer two scrapes.
func byPlugin(apps []string) [][]string {
	var out [][]string
	at := map[string]int{}
	for _, app := range apps {
		plugin, _ := pluginOf(app)
		if i, ok := at[plugin]; ok {
			out[i] = append(out[i], app)
			continue
		}
		at[plugin] = len(out)
		out = append(out, []string{app})
	}
	return out
}

// appSaying is a source's name in words, for the places a chip's glyph is no
// help: a tooltip, a briefing's prompt, what a toast calls it. x's two
// timelines share a plugin and a glyph, so this is where they are told apart in
// prose.
func appSaying(app string) string {
	if app == xForYouApp {
		return "x For You"
	}
	return app
}

// stampApp files a plugin's output under the source that asked for it. The x
// plugin says "x" whichever tab it read, and For You's items are For You's.
func stampApp(items []core.Item, app string) {
	for i := range items {
		items[i].App = app
	}
}
