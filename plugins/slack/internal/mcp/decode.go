package mcp

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/genkio/tui/plugins/slack/internal/slack"
)

// slack-mcp-server emits CSV whose headers are derived from Go struct field
// names (e.g. "ChannelID", "MsgID"). A maintainer adding csv tags could switch
// them to json-style names, so we match columns case-insensitively and accept
// several aliases per field rather than hardcoding positions.

// DecodeConversations parses the conversations_unreads summary CSV.
func DecodeConversations(csvText string) ([]slack.Conversation, error) {
	rows, err := parseCSV(csvText)
	if err != nil {
		return nil, err
	}
	out := make([]slack.Conversation, 0, len(rows))
	for _, r := range rows {
		c := slack.Conversation{
			ID:          r.get("channelid", "channel_id", "id"),
			Name:        r.get("channelname", "channel_name", "name"),
			Type:        slack.ChannelType(r.get("channeltype", "channel_type", "type")),
			UnreadCount: atoi(r.get("unreadcount", "unread_count", "unread")),
			LastRead:    r.get("lastread", "last_read"),
			Latest:      r.get("latest"),
		}
		if c.ID == "" {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// DecodeMessages parses a message CSV (history, replies, or unread messages)
// and returns the messages plus the pagination cursor, if any. The cursor is
// carried on the last row's Cursor column and is empty when no pages remain.
func DecodeMessages(csvText string) ([]slack.Message, string, error) {
	rows, err := parseCSV(csvText)
	if err != nil {
		return nil, "", err
	}
	out := make([]slack.Message, 0, len(rows))
	var cursor string
	for _, r := range rows {
		if c := r.get("cursor"); c != "" {
			cursor = c
		}
		m := slack.Message{
			ID:        r.get("msgid", "msg_id", "ts", "id"),
			UserID:    r.get("userid", "user_id", "user"),
			UserName:  r.get("username", "user_name"),
			RealName:  r.get("realname", "real_name"),
			Channel:   r.get("channel"),
			ThreadTS:  r.get("threadts", "thread_ts"),
			Text:      r.get("text"),
			Permalink: r.get("permalink"),
			Reactions: r.get("reactions"),
			BotName:   r.get("botname", "bot_name"),
		}
		if m.ID == "" && m.Text == "" {
			continue
		}
		out = append(out, m)
	}
	return out, cursor, nil
}

// row is one CSV record keyed by lower-cased header name.
type row map[string]string

// get returns the first non-empty value among the given column aliases.
func (r row) get(aliases ...string) string {
	for _, a := range aliases {
		if v := strings.TrimSpace(r[a]); v != "" {
			return v
		}
	}
	return ""
}

func parseCSV(text string) ([]row, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1 // tolerate ragged rows (text fields may contain commas/newlines)

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing server CSV: %w", err)
	}
	if len(records) < 2 {
		return nil, nil // header only, or empty
	}

	header := records[0]
	rows := make([]row, 0, len(records)-1)
	for _, rec := range records[1:] {
		r := make(row, len(header))
		for i, name := range header {
			if i < len(rec) {
				r[strings.ToLower(strings.TrimSpace(name))] = rec[i]
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
