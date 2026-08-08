package douban

import (
	"strings"
	"testing"
	"time"
)

// fixture mirrors the real homepage markup: a subject mark (想读 + book card),
// a plain saying, and a reshare quoting the original with a topic card.
const homeFixture = `<!doctype html><html><body>
<div class="new-status status-wrapper">
  <div class="status-item" data-sid="9445279814" data-uid="1322447">
    <div class="mod" data-status-id="9445279814">
      <div class="hd " data-status-url="https://www.douban.com/people/1322447/status/9445279814/?_spm_id=MTMyMjQ0Nw">
        <div class="text">
          <a href="https://www.douban.com/people/jojoorc/" class="lnk-people">JOJOORC/Z</a>
          想读
        </div>
      </div>
      <div class="bd book">
        <div class="block block-subject">
          <div class="content">
            <div class="title">
              <a href="https://book.douban.com/subject/30295288/" target="_blank">21世紀的21堂課</a>
              <strong class="rating_num">8.8</strong>
            </div>
            <p>在一個資訊滿滿卻多半無用的世界上…</p>
          </div>
        </div>
        <div class="actions">
          <span class="created_at" title="2026-08-08 06:24:20"><a href="#">2小时前</a></span>
        </div>
      </div>
    </div>
  </div>
</div>
<div class="new-status status-wrapper saying">
  <div class="status-item" data-sid="9445371360" data-uid="42">
    <div class="mod">
      <div class="hd " data-status-url="https://www.douban.com/people/42/status/9445371360/?_spm_id=x">
        <div class="text">
          <a href="https://www.douban.com/people/abei/" class="lnk-people">阿北</a>
          说
          <blockquote>今天天气不错</blockquote>
        </div>
      </div>
      <div class="bd ">
        <div class="actions">
          <span class="created_at" title="2026-08-08 07:14:31"><a href="#">1小时前</a></span>
        </div>
      </div>
    </div>
  </div>
</div>
<div class="new-status status-wrapper status-reshared-wrapper saying">
  <div class="status-item" data-sid="9444407389" data-uid="1011741">
    <div class="mod">
      <div class="hd reshared_hd" data-status-url="https://www.douban.com/people/1011741/status/9444407389/?_spm_id=y">
        <div class="text">
          <a href="https://www.douban.com/people/NullPointer/" class="lnk-people">NullPointer</a>
          转发：
          <blockquote>帮亲不帮理吧</blockquote>
        </div>
      </div>
      <div class="bd ">
        <div class="actions">
          <span class="created_at" title="2026-08-08 00:50:30"><a href="#">今天凌晨</a></span>
        </div>
      </div>
    </div>
  </div>
  <div class="status-real-wrapper" data-sid="9444373658">
    <div class="status-item" data-sid="9444373658" data-uid="1011741">
      <div class="mod">
        <div class="hd " data-status-url="https://www.douban.com/people/1011741/status/9444373658/?_spm_id=y">
          <div class="text">
            <a href="https://www.douban.com/people/orig/" class="lnk-people">原作者</a>
            <blockquote><p>原帖内容</p></blockquote>
          </div>
        </div>
        <div class="bd rec">
          <div class="block group-topic-block" data-url="https://douc.cc/f9P8dB">
            <div class="content">
              <div class="title"><a href="https://douc.cc/f9P8dB" target="_blank">为什么回应的往往是女作家</a></div>
              <p>我在公众号上读到了她的自述…</p>
            </div>
          </div>
          <div class="actions">
            <span class="created_at" title="2026-08-08 00:44:59"><a href="#">今天凌晨</a></span>
          </div>
        </div>
      </div>
    </div>
  </div>
</div>
<div class="new-status status-wrapper" data-uid="1137591" data-sid="9444222066" data-atype="topic">
  <div class="status-item" data-uid="1137591" data-sid="9444222066" data-atype="topic">
    <div class="mod">
      <div class="hd">
        <div class="text">
          <a href="https://www.douban.com/people/1137591/" class="lnk-people">dexteryy</a>
          <span type="topic">说：</span>
        </div>
      </div>
      <div class="bd">
        <div class="content">
          <div class="title"><a href="https://www.douban.com/topic/496340053/?_spm_id=MTEzNzU5MQ"></a></div>
          <blockquote><p style="white-space: pre-line;">为了让技术小白理解Cloudflare在AI时代的意义</p></blockquote>
        </div>
        <div class="actions">
          <span class="created_at" title="2026-08-08 00:21:52"><a href="https://www.douban.com/topic/496340053/?_spm_id=MTEzNzU5MQ">今天凌晨</a></span>
        </div>
      </div>
    </div>
  </div>
</div>
<div class="new-status status-wrapper" data-uid="34500041" data-sid="9442567297" data-atype="topic" data-subtype="group">
  <div class="status-item" data-uid="34500041" data-sid="9442567297" data-atype="topic" data-subtype="group">
    <div class="mod">
      <div class="hd">
        <div class="text">
          <a href="https://www.douban.com/people/34500041/" class="lnk-people">一心而用</a>
          在 <a href="https://www.douban.com/group/749646/">BanG Dream!小组</a>
          <span type="topic"></span>
        </div>
      </div>
      <div class="bd">
        <div class="content">
          <div class="title"><a href="https://www.douban.com/group/topic/496312256/?_spm_id=MzQ1MDAwNDE">藤都子老师新作</a></div>
        </div>
        <div class="actions">
          <span class="created_at" title="2026-08-07 21:00:00"><a href="https://www.douban.com/group/topic/496312256/?_spm_id=MzQ1MDAwNDE">昨天</a></span>
        </div>
      </div>
    </div>
  </div>
</div>
<div class="new-status status-wrapper" data-uid="2316731" data-sid="9444111442" data-atype="photo">
  <div class="status-item" data-uid="2316731" data-sid="9444111442" data-atype="photo">
    <div class="mod">
      <div class="hd">
        <div class="text">
          <a href="https://www.douban.com/people/2316731/" class="lnk-people">法兰西胶片</a>
          <span type="photo">上传照片到</span>
          <a href="/photos/album/51484537/">宅累补完计划</a>
        </div>
      </div>
      <div class="bd">
        <div class="block-photo mode-2"><div class="pic group-pics"></div></div>
        <div class="actions">
          <span class="created_at" title="2026-08-08 00:05:52"><a href="https://www.douban.com/photos/photo/2934763947/">今天凌晨</a></span>
        </div>
      </div>
    </div>
  </div>
</div>
</body></html>`

func TestParseHome(t *testing.T) {
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	statuses, err := parseHome([]byte(homeFixture), now)
	if err != nil {
		t.Fatalf("parseHome: %v", err)
	}
	if len(statuses) != 6 {
		t.Fatalf("expected 6 statuses, got %d: %+v", len(statuses), statuses)
	}

	mark := statuses[0]
	if mark.ID != "9445279814" || mark.Author != "JOJOORC/Z" || mark.Activity != "想读" {
		t.Errorf("subject mark fields wrong: %+v", mark)
	}
	if !strings.Contains(mark.Text, "→ 21世紀的21堂課") ||
		!strings.Contains(mark.Text, "https://book.douban.com/subject/30295288/") ||
		!strings.Contains(mark.Text, "在一個資訊滿滿") {
		t.Errorf("book card not folded into text: %q", mark.Text)
	}
	// tracking params are stripped from the status URL
	if mark.URL != "https://www.douban.com/people/1322447/status/9445279814/" {
		t.Errorf("url wrong: %q", mark.URL)
	}
	// created_at titles are Beijing wall clock (UTC+8): 06:24 CST == 22:24 UTC (prev day)
	if !mark.CreatedAt.Equal(time.Date(2026, 8, 7, 22, 24, 20, 0, time.UTC)) {
		t.Errorf("createdAt wrong: %v", mark.CreatedAt)
	}
	if mark.Age != "1h" {
		t.Errorf("age wrong: %q", mark.Age)
	}

	say := statuses[1]
	if say.Author != "阿北" || say.Activity != "说" || say.Text != "今天天气不错" {
		t.Errorf("saying fields wrong: %+v", say)
	}

	re := statuses[2]
	if re.ID != "9444407389" || re.Author != "NullPointer" || re.Activity != "转发" {
		t.Errorf("reshare fields wrong: %+v", re)
	}
	if !strings.Contains(re.Text, "帮亲不帮理吧") {
		t.Errorf("resharer's comment missing: %q", re.Text)
	}
	if !strings.Contains(re.Text, "↻ 原作者: 原帖内容") {
		t.Errorf("original not quoted: %q", re.Text)
	}
	if !strings.Contains(re.Text, "→ 为什么回应的往往是女作家") || !strings.Contains(re.Text, "https://douc.cc/f9P8dB") {
		t.Errorf("original's topic card missing: %q", re.Text)
	}

	// topic (动态) variant: saying lives in .bd .content, URL on the timestamp link
	topic := statuses[3]
	if topic.Author != "dexteryy" || topic.Activity != "说" {
		t.Errorf("topic saying fields wrong: %+v", topic)
	}
	if !strings.Contains(topic.Text, "为了让技术小白理解Cloudflare在AI时代的意义") {
		t.Errorf("topic saying text missing: %q", topic.Text)
	}
	if topic.URL != "https://www.douban.com/topic/496340053/" {
		t.Errorf("topic url wrong: %q", topic.URL)
	}

	// group topic: the linked title is the content, tracking query stripped
	group := statuses[4]
	if group.Activity != "在 BanG Dream!小组" {
		t.Errorf("group activity wrong: %+v", group)
	}
	if !strings.Contains(group.Text, "→ 藤都子老师新作") ||
		!strings.Contains(group.Text, "https://www.douban.com/group/topic/496312256/") ||
		strings.Contains(group.Text, "_spm_id") {
		t.Errorf("group topic title/url wrong: %q", group.Text)
	}

	// photo upload: no text at all; ToItem falls back to the activity
	photo := statuses[5]
	if photo.Text != "" || photo.Activity != "上传照片到 宅累补完计划" {
		t.Errorf("photo fields wrong: %+v", photo)
	}
	if it := ToItem(photo); it.Title != "上传照片到 宅累补完计划" {
		t.Errorf("empty-text status should title as its activity: %q", it.Title)
	}
	if photo.URL != "https://www.douban.com/photos/photo/2934763947/" {
		t.Errorf("photo url wrong: %q", photo.URL)
	}
}

func TestParseHomeLoggedOut(t *testing.T) {
	// the logged-out landing page has no status stream: zero statuses, no error
	// (the client turns that into a stale-session error using the login links)
	statuses, err := parseHome([]byte(`<html><body><a href="https://accounts.douban.com/passport/login">登录</a></body></html>`), time.Now())
	if err != nil || len(statuses) != 0 {
		t.Fatalf("expected empty parse for logged-out page, got %d, %v", len(statuses), err)
	}
}

func TestToItem(t *testing.T) {
	st := Status{
		ID:       "42",
		Author:   "阿北",
		Activity: "说",
		Text:     "hello",
		URL:      "https://www.douban.com/people/alice/status/42/",
		Age:      "2h",
	}
	it := ToItem(st)
	if it.App != "douban" || it.ID != "42" {
		t.Errorf("identity wrong: %+v", it)
	}
	// full text rides in both title and body (x-style), so the merged web view
	// dedups it into a single body block
	if it.Title != "hello" || it.Body != "hello" {
		t.Errorf("title/body wrong: %+v", it)
	}
	if it.Source != "阿北 说" {
		t.Errorf("source should carry author + activity: %q", it.Source)
	}
}
