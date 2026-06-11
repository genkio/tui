// Package slack holds the domain types the TUI works with. They are a small,
// stable projection of whatever slack-mcp-server returns, so the rest of the
// app never deals with CSV rows or raw API shapes.
package slack

import (
	"strconv"
	"strings"
	"time"
)

// ChannelType classifies a conversation, mirroring slack-mcp-server's
// conversations_unreads output.
type ChannelType string

const (
	TypeDM       ChannelType = "dm"
	TypeGroupDM  ChannelType = "group_dm"
	TypePartner  ChannelType = "partner"  // shared / external-org channels
	TypeInternal ChannelType = "internal" // regular workspace channels
)

// Label returns a short human-readable name for the type.
func (t ChannelType) Label() string {
	switch t {
	case TypeDM:
		return "DM"
	case TypeGroupDM:
		return "Group"
	case TypePartner:
		return "External"
	case TypeInternal:
		return "Channel"
	default:
		return string(t)
	}
}

// Conversation is one unread channel or DM from conversations_unreads.
type Conversation struct {
	ID          string
	Name        string
	Type        ChannelType
	UnreadCount int
	LastRead    string // Slack ts the user has read up to
	Latest      string // Slack ts of the most recent message
}

// Message is one message from history, a thread, or the unread output.
type Message struct {
	ID        string // Slack ts, e.g. "1700000000.123456"
	UserID    string
	UserName  string
	RealName  string
	Channel   string
	ThreadTS  string
	Text      string
	Permalink string
	Reactions string
	BotName   string
}

// Author is the best display name available for the message's sender.
func (m Message) Author() string {
	switch {
	case m.RealName != "":
		return m.RealName
	case m.UserName != "":
		return m.UserName
	case m.BotName != "":
		return m.BotName
	case m.UserID != "":
		return m.UserID
	default:
		return "unknown"
	}
}

// Time parses the Slack ts into a time. The ts is the source of truth for
// when a message was sent, so we derive the timestamp from it rather than
// trusting a separately formatted field.
func (m Message) Time() time.Time { return ParseTS(m.ID) }

// IsThreadRoot reports whether this message is a thread parent that has
// replies. Slack only sets thread_ts on the parent once a thread exists, so
// thread_ts == ts reliably means "has replies". The server does not expose a
// reply count, so this is how we know a thread can be expanded.
func (m Message) IsThreadRoot() bool {
	return m.ThreadTS != "" && m.ThreadTS == m.ID
}

// IsReply reports whether the message lives inside someone else's thread.
func (m Message) IsReply() bool {
	return m.ThreadTS != "" && m.ThreadTS != m.ID
}

// ParseTS converts a Slack timestamp ("seconds.micros") into a time.Time in
// the local zone. It returns the zero time for empty or malformed input.
func ParseTS(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	secStr, fracStr, _ := strings.Cut(ts, ".")
	sec, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		return time.Time{}
	}
	var nsec int64
	if fracStr != "" {
		if micros, err := strconv.ParseInt(fracStr, 10, 64); err == nil {
			nsec = micros * 1000 // Slack fractions are microseconds
		}
	}
	return time.Unix(sec, nsec).Local()
}
