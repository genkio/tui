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
	// a subject the user marked is the status's own content, not an embed
	if mark.Embed != nil {
		t.Errorf("a 想读 mark should keep its book card in the text: %+v", mark.Embed)
	}

	say := statuses[1]
	if say.Author != "阿北" || say.Activity != "说" || say.Text != "今天天气不错" {
		t.Errorf("saying fields wrong: %+v", say)
	}

	re := statuses[2]
	if re.ID != "9444407389" || re.Author != "NullPointer" || re.Activity != "转发" {
		t.Errorf("reshare fields wrong: %+v", re)
	}
	// the resharer's own comment is the status text; the original embeds
	if re.Text != "帮亲不帮理吧" {
		t.Errorf("resharer's comment should stand alone: %q", re.Text)
	}
	if re.Embed == nil {
		t.Fatalf("the reshared original should embed: %+v", re)
	}
	if re.Embed.Source != "原作者" {
		t.Errorf("embed source wrong: %q", re.Embed.Source)
	}
	if re.Embed.URL != "https://www.douban.com/people/1011741/status/9444373658/" {
		t.Errorf("embed url wrong: %q", re.Embed.URL)
	}
	if !strings.Contains(re.Embed.Text, "原帖内容") {
		t.Errorf("original's words missing from the embed: %q", re.Embed.Text)
	}
	if !strings.Contains(re.Embed.Text, "→ 为什么回应的往往是女作家") || !strings.Contains(re.Embed.Text, "https://douc.cc/f9P8dB") {
		t.Errorf("original's topic card missing from the embed: %q", re.Embed.Text)
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

// Douban lazy-loads pictures, leaving the real file in data-src. The poster's
// avatar is chrome, not content, so it must not turn up as an attachment; a
// reshare contributes the original's pictures.
func TestParseHomePhotos(t *testing.T) {
	fixture := `<html><body>
<div class="new-status status-wrapper" data-uid="1" data-sid="900" data-atype="photo">
  <div class="status-item" data-uid="1" data-sid="900" data-atype="photo">
    <div class="usr-pic"><a href="/people/alice/"><img src="https://img1.doubanio.com/icon/u1-1.jpg"></a></div>
    <div class="mod">
      <div class="hd" data-status-url="https://www.douban.com/people/alice/status/900/">
        <div class="text"><a href="/people/alice/" class="lnk-people">alice</a><span>说</span></div>
      </div>
      <div class="bd">
        <blockquote><p>看图</p></blockquote>
        <div class="block-photo mode-2"><div class="pic">
          <img src="https://img1.doubanio.com/blank.gif" data-src="https://img9.doubanio.com/view/status/l/public/one.jpg">
          <img data-src="https://img9.doubanio.com/view/status/l/public/two.jpg">
        </div></div>
        <div class="actions"><span class="created_at" title="2026-08-08 00:05:52">今天</span></div>
      </div>
    </div>
  </div>
  <div class="status-real-wrapper">
    <div class="status-item" data-uid="2" data-sid="901">
      <div class="usr-pic"><img src="https://img1.doubanio.com/icon/u2-1.jpg"></div>
      <div class="mod">
        <div class="hd"><div class="text"><a class="lnk-people">bob</a></div></div>
        <div class="bd"><div class="block-photo"><div class="pic">
          <img data-src="https://img9.doubanio.com/view/status/l/public/orig.jpg">
        </div></div></div>
      </div>
    </div>
  </div>
</div>
</body></html>`

	statuses, err := parseHome([]byte(fixture), time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("parseHome: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	want := []string{
		"https://img9.doubanio.com/view/status/l/public/one.jpg",
		"https://img9.doubanio.com/view/status/l/public/two.jpg",
	}
	got := statuses[0].Images
	if len(got) != len(want) {
		t.Fatalf("images = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("images = %v, want %v", got, want)
		}
	}
	for _, u := range got {
		if strings.Contains(u, "/icon/") {
			t.Errorf("an avatar leaked into the attachments: %q", u)
		}
	}
	// the reshared post keeps its own picture, so the card can draw it inside
	// the embedded block rather than among the resharer's stills
	embed := statuses[0].Embed
	if embed == nil || len(embed.Images) != 1 || embed.Images[0] != "https://img9.doubanio.com/view/status/l/public/orig.jpg" {
		t.Fatalf("original's picture should ride with the embed: %+v", embed)
	}
	if strings.Contains(strings.Join(embed.Images, " "), "/icon/") {
		t.Errorf("an avatar leaked into the embed: %v", embed.Images)
	}
	if len(ToItem(statuses[0]).Images) != 2 {
		t.Error("ToItem dropped the pictures")
	}
}

// Resharing a group discussion brings no original status wrapper: the
// discussion rides along in a div.topic-card, whose title links through a
// /link2/ bounce and whose pictures reach the page only as the gallery
// script's JSON. Markup trimmed from the real homepage entry for
// https://www.douban.com/people/2298386/status/9457779094/
func TestParseHomeReshareDiscussion(t *testing.T) {
	fixture := `<html><body>
<div class="new-status status-wrapper" data-sid="9457779094" data-uid="2298386">
  <div class="status-item" data-sid="9457779094" data-uid="2298386" data-target-type="rec">
    <div class="mod" data-status-id="9457779094">
      <div class="hd " data-status-url="https://www.douban.com/people/2298386/status/9457779094/?_spm_id=MjI5ODM4Ng">
        <div class="usr-pic"><a href="https://www.douban.com/people/MoNoMilky/"><img src="https://img9.doubanio.com/icon/up2298386-156.jpg"></a></div>
        <div class="text">
          <a href="https://www.douban.com/people/MoNoMilky/" class="lnk-people">竹子哟竹子✨</a>
          转发了 <a target="_blank" href="https://www.douban.com/group/586674/">生活组</a> 的讨论：
          <blockquote><p>。。。</p></blockquote>
        </div>
      </div>
      <div class="bd rec">
        <div class="topic-card large-card" data-url="https://www.douban.com/link2/?url=https%3A%2F%2Fwww.douban.com%2Fgroup%2Ftopic%2F496184018%2F%3F_spm_id%3DMTk1MzUwMzQz">
          <div class="topic-card-owner"><a href="https://www.douban.com/people/195350343/">momo</a> 说：</div>
          <div class="topic-card-title">
            <a href="https://www.douban.com/link2/?url=https%3A%2F%2Fwww.douban.com%2Fgroup%2Ftopic%2F496184018%2F%3F_spm_id%3DMTk1MzUwMzQz">好讨厌平台</a>
          </div>
          <blockquote><p style="white-space: pre-line;">我们在抖音上卖东西，平台抽佣8%，
现在出了新规定，客人通过豆包搜索后，下单了我们的产品，豆包抽佣12%</p></blockquote>
          <div class="pics-wrapper">
            <script>
              var photos = [{"image": {"normal": {"url": "https://img2.doubanio.com/view/group_topic/l/public/p742204311.jpg", "width": 500}, "large": {"url": "https://img2.doubanio.com/view/group_topic/l/public/p742204311.jpg", "width": 500}, "raw": null}, "uri": ""}, {"image": {"normal": {"url": "https://img3.doubanio.com/view/group_topic/l/public/p742204312.jpg", "width": 500}, "large": {"url": "https://img3.doubanio.com/view/group_topic/l/public/p742204312.jpg", "width": 500}, "raw": null}, "uri": ""}];
              if (window.CREATE_HONRIZONTAL_PHOTOS) { window.CREATE_HONRIZONTAL_PHOTOS({photos: photos}) }
            </script>
          </div>
        </div>
        <div class="actions">
          <span class="created_at" title="2026-08-09 18:58:17"><a href="#">1小时前</a></span>
        </div>
      </div>
    </div>
  </div>
</div>
</body></html>`

	statuses, err := parseHome([]byte(fixture), time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("parseHome: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	s := statuses[0]
	if s.Activity != "转发了 生活组 的讨论" {
		t.Errorf("activity wrong: %q", s.Activity)
	}
	// the discussion's own blockquote must not be mistaken for the resharer's
	if s.Text != "。。。" {
		t.Errorf("only the resharer's comment belongs in the text: %q", s.Text)
	}
	if len(s.Images) != 0 {
		t.Errorf("the discussion's pictures belong to the embed, not the status: %v", s.Images)
	}
	if s.Embed == nil {
		t.Fatalf("the reshared discussion should embed: %+v", s)
	}
	if s.Embed.Source != "好讨厌平台" || s.Embed.Author != "momo" {
		t.Errorf("embed headline/owner wrong: %+v", s.Embed)
	}
	// the /link2/ bounce is unwrapped, then its tracking query dropped
	if s.Embed.URL != "https://www.douban.com/group/topic/496184018/" {
		t.Errorf("embed link wrong: %q", s.Embed.URL)
	}
	if !strings.Contains(s.Embed.Text, "我们在抖音上卖东西") {
		t.Errorf("embed body wrong: %q", s.Embed.Text)
	}
	// img2/img3 in the fixture: the mainland CDNs are swapped for the global one
	want := []string{
		"https://img9.doubanio.com/view/group_topic/l/public/p742204311.jpg",
		"https://img9.doubanio.com/view/group_topic/l/public/p742204312.jpg",
	}
	if len(s.Embed.Images) != 2 || s.Embed.Images[0] != want[0] || s.Embed.Images[1] != want[1] {
		t.Errorf("embed pictures = %v, want %v", s.Embed.Images, want)
	}
	if q := ToItem(s).Quote; q == nil || q.Source != "好讨厌平台" {
		t.Errorf("ToItem should carry the embed through as the item's quote: %+v", q)
	}
}

// Pictures attached to an ordinary status arrive the same way: a gallery script
// rather than <img> tags, so reading tags alone loses them.
func TestParseHomeScriptPhotos(t *testing.T) {
	fixture := `<html><body>
<div class="new-status status-wrapper" data-sid="9457641587" data-uid="1">
  <div class="status-item" data-sid="9457641587" data-uid="1">
    <div class="mod">
      <div class="hd" data-status-url="https://www.douban.com/people/alice/status/9457641587/">
        <div class="text"><a class="lnk-people" href="/people/alice/">alice</a>说<blockquote><p>看图</p></blockquote></div>
      </div>
      <div class="bd">
        <div class="pics-wrapper"><script>
          var photos = [{"image": {"normal": {"url": "https://img1.doubanio.com/view/group_topic/l/public/one.jpg"}, "large": {"url": "https://img1.doubanio.com/view/group_topic/l/public/one-large.jpg"}}}];
        </script></div>
        <div class="actions"><span class="created_at" title="2026-08-09 18:00:00">1小时前</span></div>
      </div>
    </div>
  </div>
</div>
</body></html>`

	statuses, err := parseHome([]byte(fixture), time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("parseHome: %v", err)
	}
	// the large variant wins: the card scales it down anyway
	got := statuses[0].Images
	if len(got) != 1 || got[0] != "https://img9.doubanio.com/view/group_topic/l/public/one-large.jpg" {
		t.Errorf("images = %v, want the script's large url", got)
	}
}

// douban spreads its pictures over four CDNs behind img1/img2/img3/img9. Only
// img9 answers from outside China, and the digit means nothing else, so every
// picture is asked of that one.
func TestImageURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://img1.doubanio.com/view/photo/m/public/p1.jpg", "https://img9.doubanio.com/view/photo/m/public/p1.jpg"},
		{"https://img3.doubanio.com/view/status/small/public/x.jpg", "https://img9.doubanio.com/view/status/small/public/x.jpg"},
		{"https://img9.doubanio.com/view/photo/m/public/p1.jpg", "https://img9.doubanio.com/view/photo/m/public/p1.jpg"},
		// protocol-relative first becomes absolute, then normalizes
		{"//img2.doubanio.com/view/photo/m/public/p1.jpg", "https://img9.doubanio.com/view/photo/m/public/p1.jpg"},
		// only the numbered image hosts are douban's CDN shards
		{"https://www.douban.com/img1.doubanio.com/nope.jpg", "https://www.douban.com/img1.doubanio.com/nope.jpg"},
		{"https://example.com/p.jpg", "https://example.com/p.jpg"},
		{"", ""},
	}
	for _, c := range cases {
		if got := imageURL(c.in); got != c.want {
			t.Errorf("imageURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUnwrapLink2(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"https://www.douban.com/link2/?url=https%3A%2F%2Fwww.douban.com%2Fgroup%2Ftopic%2F496184018%2F",
			"https://www.douban.com/group/topic/496184018/",
		},
		// not a bounce: left alone, query and all
		{"https://book.douban.com/subject/30295288/", "https://book.douban.com/subject/30295288/"},
		// a bounce with nothing to unwrap stays as it is rather than becoming empty
		{"https://www.douban.com/link2/", "https://www.douban.com/link2/"},
		{"", ""},
	}
	for _, c := range cases {
		if got := unwrapLink2(c.in); got != c.want {
			t.Errorf("unwrapLink2(%q) = %q, want %q", c.in, got, c.want)
		}
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
