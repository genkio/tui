package x

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Tab selects which home timeline to read.
type Tab int

const (
	ForYou    Tab = iota // the algorithmic feed (HomeTimeline)
	Following            // the reverse-chronological feed (HomeLatestTimeline)
)

func (t Tab) String() string {
	if t == Following {
		return "Following"
	}
	return "For You"
}

// publicBearer is the web app's hardcoded OAuth2 bearer, identical for every
// user; the real auth is the cookie + csrf, not this token.
const publicBearer = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs=1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"

// defaultUA mimics the web client; x.com is unhappy with a missing User-Agent.
const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

// queryIDs and the feature flags below are lifted from the live web app and
// rotate when x.com redeploys. If a timeline starts failing with an "unknown
// query id" or a "feature ... must be defined" error, re-capture them (open the
// site, read /i/api/graphql/.../HomeTimeline off the network panel).
const (
	homeTimelineQueryID       = "yXkXiX5sZhWEsOjz4u9dSQ"
	homeLatestTimelineQueryID = "3EReKHnXX2ebBZP1afZnXw"
)

const timelineFeatures = `{"rweb_video_screen_enabled":false,"rweb_cashtags_enabled":true,"profile_label_improvements_pcf_label_in_post_enabled":true,"responsive_web_profile_redirect_enabled":false,"rweb_tipjar_consumption_enabled":false,"verified_phone_label_enabled":false,"creator_subscriptions_tweet_preview_api_enabled":true,"responsive_web_graphql_timeline_navigation_enabled":true,"responsive_web_graphql_skip_user_profile_image_extensions_enabled":false,"premium_content_api_read_enabled":false,"communities_web_enable_tweet_community_results_fetch":true,"c9s_tweet_anatomy_moderator_badge_enabled":true,"responsive_web_grok_analyze_button_fetch_trends_enabled":false,"responsive_web_grok_analyze_post_followups_enabled":true,"rweb_cashtags_composer_attachment_enabled":true,"responsive_web_jetfuel_frame":true,"responsive_web_grok_share_attachment_enabled":true,"responsive_web_grok_annotations_enabled":true,"articles_preview_enabled":true,"responsive_web_edit_tweet_api_enabled":true,"rweb_conversational_replies_downvote_enabled":false,"graphql_is_translatable_rweb_tweet_is_translatable_enabled":true,"view_counts_everywhere_api_enabled":true,"longform_notetweets_consumption_enabled":true,"responsive_web_twitter_article_tweet_consumption_enabled":true,"content_disclosure_indicator_enabled":true,"content_disclosure_ai_generated_indicator_enabled":true,"responsive_web_grok_show_grok_translated_post":true,"responsive_web_grok_analysis_button_from_backend":true,"post_ctas_fetch_enabled":true,"freedom_of_speech_not_reach_fetch_enabled":true,"standardized_nudges_misinfo":true,"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled":true,"longform_notetweets_rich_text_read_enabled":true,"longform_notetweets_inline_media_enabled":false,"responsive_web_grok_image_annotation_enabled":true,"responsive_web_grok_imagine_annotation_enabled":true,"responsive_web_grok_community_note_auto_translation_is_enabled":true,"responsive_web_enhance_cards_enabled":false}`

// Client reads home timelines from x.com's web GraphQL API.
type Client struct {
	hc        *http.Client
	base      string
	bearer    string
	authToken string
	csrf      string
	lang      string
}

// New builds a client from a captured browser session. bearer and lang fall
// back to the public web defaults when empty.
func New(authToken, csrf, bearer, lang string) *Client {
	if bearer == "" {
		bearer = publicBearer
	}
	if lang == "" {
		lang = "en"
	}
	return &Client{
		hc:        &http.Client{Timeout: 30 * time.Second},
		base:      "https://x.com/i/api/graphql",
		bearer:    bearer,
		authToken: authToken,
		csrf:      csrf,
		lang:      lang,
	}
}

// Timeline fetches up to count posts from the given home tab.
func (c *Client) Timeline(ctx context.Context, tab Tab, count int) ([]Tweet, error) {
	if count <= 0 {
		count = 20
	}
	qid, op := homeTimelineQueryID, "HomeTimeline"
	vars := map[string]any{
		"count":                  count,
		"includePromotedContent": true,
		"requestContext":         "launch",
		"withCommunity":          true,
	}
	if tab == Following {
		qid, op = homeLatestTimelineQueryID, "HomeLatestTimeline"
		vars["latestControlAvailable"] = true
	}
	varsJSON, err := json.Marshal(vars)
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/%s/%s?variables=%s&features=%s",
		c.base, qid, op, url.QueryEscape(string(varsJSON)), url.QueryEscape(timelineFeatures))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("x.com rejected the session (HTTP %d); re-run make auth", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("x.com rate limit hit (HTTP 429); wait a bit before refreshing")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("x.com returned HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	return parseTimeline(body)
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("authorization", "Bearer "+c.bearer)
	req.Header.Set("x-csrf-token", c.csrf)
	req.Header.Set("x-twitter-active-user", "yes")
	req.Header.Set("x-twitter-auth-type", "OAuth2Session")
	req.Header.Set("x-twitter-client-language", c.lang)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "*/*")
	req.Header.Set("user-agent", defaultUA)
	req.Header.Set("referer", "https://x.com/home")
	// auth_token authenticates; ct0 must equal the x-csrf-token header.
	req.Header.Set("cookie", "auth_token="+c.authToken+"; ct0="+c.csrf)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
