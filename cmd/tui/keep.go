package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/genkio/tui/core"
)

// keepDirName is the folder under the sync dir the footer's keep saves into.
const keepDirName = "videos"

// keeps is the server-side keep, nil when there is no download server to
// hand downloads to; the footer's keep link then falls back to a browser
// download through /dl.
var keeps *keeper

// keeper hands a card's video to a download server (see the README for the two
// routes it answers), which runs on this machine and leaves the file in dir,
// named after the video. Which file is whose is written down in dir's index (keepIndexName), so
// a restart, or another machine syncing the same folder, knows without being
// told; a video is kept while its file is still there. What a keep in flight is
// doing, or why it failed, lives here until the next restart.
type keeper struct {
	api    string // the download server's origin
	dir    string
	client *http.Client
	poll   time.Duration

	mu   sync.Mutex
	runs map[string]*keepRun // by name
}

// keepIndexName is the file in the keep folder mapping each keep's name to the
// file it saved. Hidden, so it reads as part of the folder rather than of it.
const keepIndexName = ".keeps.json"

type keepRun struct {
	state string // keepPending or keepError; a finished one is its file
	err   string
}

const (
	keepNone    = ""
	keepPending = "keeping"
	keepDone    = "kept"
	keepError   = "error"
)

// newKeeperFromEnv is the keeper TUI_KEEP_URL asks for, nil when it is unset
// or there is no sync dir to keep into.
func newKeeperFromEnv() *keeper {
	api := strings.TrimRight(strings.TrimSpace(os.Getenv("TUI_KEEP_URL")), "/")
	syncDir := core.SyncDir()
	if api == "" || syncDir == "" {
		return nil
	}
	return newKeeper(api, filepath.Join(syncDir, keepDirName))
}

func newKeeper(api, dir string) *keeper {
	return &keeper{
		api:    api,
		dir:    dir,
		client: &http.Client{Timeout: 15 * time.Second},
		poll:   2 * time.Second,
		runs:   map[string]*keepRun{},
	}
}

// keepStatus is what the page is told about one keep.
type keepStatus struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// states reads the folder once for a whole page of cards.
func (k *keeper) states() map[string]keepStatus {
	out := map[string]keepStatus{}
	for n := range k.keptFiles() {
		out[n] = keepStatus{State: keepDone}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for n, r := range k.runs {
		if _, done := out[n]; !done {
			out[n] = keepStatus{State: r.state, Error: r.err}
		}
	}
	return out
}

func (k *keeper) status(name string) keepStatus {
	if k.kept(name) {
		return keepStatus{State: keepDone}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if r := k.runs[name]; r != nil {
		return keepStatus{State: r.state, Error: r.err}
	}
	return keepStatus{State: keepNone}
}

func (k *keeper) kept(name string) bool {
	_, ok := k.keptFiles()[name]
	return ok
}

// keptFiles is the index less the entries whose file has since been deleted
// or renamed, which are no longer kept.
func (k *keeper) keptFiles() map[string]string {
	idx := k.readIndex()
	for n, file := range idx {
		if _, err := os.Stat(filepath.Join(k.dir, file)); err != nil {
			delete(idx, n)
		}
	}
	return idx
}

func (k *keeper) readIndex() map[string]string {
	idx := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(k.dir, keepIndexName)); err == nil {
		json.Unmarshal(data, &idx)
	}
	return idx
}

// record writes down which file a finished keep saved.
func (k *keeper) record(name, file string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	idx := k.readIndex()
	idx[name] = file
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(k.dir, keepIndexName)
	if err := os.WriteFile(path+".tmp", data, 0o644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// start asks the download server for the video at src and watches the download until it
// ends. A second tap while one is running, or on a video already kept, starts
// nothing.
func (k *keeper) start(name, src, title string) keepStatus {
	if st := k.status(name); st.State == keepDone || st.State == keepPending {
		return st
	}
	k.set(name, keepPending, "")
	id, err := k.submit(name, src, title)
	if err != nil {
		k.set(name, keepError, err.Error())
		return k.status(name)
	}
	go k.watch(name, id)
	return keepStatus{State: keepPending}
}

type saveJob struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Path   string `json:"path"`
	Error  string `json:"error"`
}

func (k *keeper) submit(name, src, title string) (string, error) {
	// The video's own title where it has one (a YouTube or bilibili watch page),
	// else the post's; the name itself when the post has no text either.
	if title == "" {
		title = name
	}
	body, _ := json.Marshal(map[string]string{"url": src, "dir": k.dir, "name": "{title}", "title": title})
	resp, err := k.client.Post(k.api+"/api/saves", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("the download server is not answering at %s: %w", k.api, err)
	}
	defer resp.Body.Close()
	var s saveJob
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return "", fmt.Errorf("download server: %s", resp.Status)
	}
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("download server: %s", s.Error)
	}
	return s.ID, nil
}

// watch polls the download server until the download ends. A few failed polls in a row
// are the download server gone, which a restart there would also make of the job.
func (k *keeper) watch(name, id string) {
	misses := 0
	for {
		time.Sleep(k.poll)
		s, err := k.fetch(id)
		if err != nil {
			if misses++; misses < 5 {
				continue
			}
			k.set(name, keepError, err.Error())
			return
		}
		misses = 0
		switch s.Status {
		case "done":
			if filepath.Dir(s.Path) != k.dir {
				k.set(name, keepError, "the download server saved it outside "+k.dir+": "+s.Path)
				return
			}
			if err := k.record(name, filepath.Base(s.Path)); err != nil {
				k.set(name, keepError, "saved "+s.Path+" but could not note it down: "+err.Error())
				return
			}
			k.clear(name)
			return
		case "error":
			k.set(name, keepError, s.Error)
			return
		}
	}
}

var errLostSave = errors.New("the download server lost track of this download (was it restarted?)")

func (k *keeper) fetch(id string) (saveJob, error) {
	var s saveJob
	resp, err := k.client.Get(k.api + "/api/saves/" + url.PathEscape(id))
	if err != nil {
		return s, fmt.Errorf("the download server stopped answering: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return s, errLostSave
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return s, fmt.Errorf("download server: %s", resp.Status)
	}
	return s, nil
}

func (k *keeper) set(name, state, msg string) {
	k.mu.Lock()
	k.runs[name] = &keepRun{state: state, err: msg}
	k.mu.Unlock()
}

func (k *keeper) clear(name string) {
	k.mu.Lock()
	delete(k.runs, name)
	k.mu.Unlock()
}

// handle is /keep: POST n and u to start one, GET ?n= to ask after it.
func (k *keeper) handle(w http.ResponseWriter, r *http.Request) {
	var st keepStatus
	switch r.Method {
	case http.MethodGet:
		name := keepName(r.URL.Query().Get("n"))
		if name == "" {
			http.Error(w, "n is required", http.StatusBadRequest)
			return
		}
		st = k.status(name)
	case http.MethodPost:
		name := keepName(r.FormValue("n"))
		src := r.FormValue("u")
		if u, err := url.Parse(src); name == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			http.Error(w, "n and an http(s) u are required", http.StatusBadRequest)
			return
		}
		st = k.start(name, src, r.FormValue("t"))
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(st)
}

// keepName is the file a keep writes, less its extension: the same characters
// /dl allows in a name, and never hidden, since a dot-file is a part-file.
func keepName(n string) string {
	return strings.TrimLeft(reDlName.ReplaceAllString(n, ""), ".")
}

// keepTitleRunes is where a post's text is cut to make a file name of it.
const keepTitleRunes = 60

var keepTitleURL = regexp.MustCompile(`https?://\S+`)

// keepTitle is the post's text as a file name would want it: the first line
// with anything in it, links taken out, cut at keepTitleRunes. The download server
// falls back to it when the video has no title of its own, which a direct mp4
// never does.
func keepTitle(texts ...string) string {
	for _, t := range texts {
		for _, line := range strings.Split(t, "\n") {
			line = strings.Join(strings.Fields(keepTitleURL.ReplaceAllString(line, "")), " ")
			if line == "" {
				continue
			}
			if r := []rune(line); len(r) > keepTitleRunes {
				line = strings.TrimSpace(string(r[:keepTitleRunes]))
			}
			return line
		}
	}
	return ""
}

// keepSource is what the download server is handed for a card's video: the clip itself
// when the card carries a direct one, else the page yt-dlp knows how to read.
func keepSource(it core.Item, video, youtube, redgif string) string {
	switch {
	case strings.HasPrefix(video, biliPath+"?"):
		if bv := biliVideoID(it.App, it.URL); bv != "" {
			return "https://www.bilibili.com/video/" + bv
		}
		return ""
	case video != "":
		return video
	case youtube != "":
		return "https://www.youtube.com/watch?v=" + youtube
	case redgif != "":
		return "https://www.redgifs.com/watch/" + strings.ToLower(redgif)
	}
	return ""
}
