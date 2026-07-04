package mcp

import "testing"

func TestDecodeConversations(t *testing.T) {
	csv := "ChannelID,ChannelName,ChannelType,UnreadCount,LastRead,Latest\n" +
		"D01,alice,dm,3,1700000000.000100,1700000300.000100\n" +
		"C09,general,internal,12,1699999999.000000,1700000999.000000\n"

	got, err := DecodeConversations(csv)
	if err != nil {
		t.Fatalf("DecodeConversations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 conversations, got %d", len(got))
	}
	if got[0].ID != "D01" || got[0].Name != "alice" || got[0].Type != "dm" || got[0].UnreadCount != 3 {
		t.Errorf("first conversation mismatch: %+v", got[0])
	}
	if got[1].Latest != "1700000999.000000" || got[1].UnreadCount != 12 {
		t.Errorf("second conversation mismatch: %+v", got[1])
	}
}

func TestDecodeMessages(t *testing.T) {
	// Header uses the server's Go-field-name columns; a quoted Text field
	// carries a comma and a newline to exercise CSV parsing.
	csv := "MsgID,UserID,UserName,RealName,Channel,ThreadTs,Text,Time,Permalink,Reactions,BotName,FileCount,AttachmentIDs,HasMedia,Cursor\n" +
		"1700000000.000100,U1,alice,Alice Smith,C09,1700000000.000100,\"hello, world\nsecond line\",,,,,0,,false,\n" +
		"1700000050.000200,U2,bob,Bob Jones,C09,1700000000.000100,a reply,,,,,0,,false,next-page-cursor\n"

	got, cursor, err := DecodeMessages(csv)
	if err != nil {
		t.Fatalf("DecodeMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 messages, got %d", len(got))
	}
	if got[0].Text != "hello, world\nsecond line" {
		t.Errorf("quoted text not parsed: %q", got[0].Text)
	}
	if got[0].Author() != "Alice Smith" {
		t.Errorf("Author() = %q, want real name", got[0].Author())
	}
	if !got[0].IsThreadRoot() {
		t.Errorf("first message should be a thread root (thread_ts == ts)")
	}
	if !got[1].IsReply() {
		t.Errorf("second message should be a reply")
	}
	if cursor != "next-page-cursor" {
		t.Errorf("cursor = %q, want next-page-cursor", cursor)
	}
}

func TestDecodeEmpty(t *testing.T) {
	convs, err := DecodeConversations("")
	if err != nil || len(convs) != 0 {
		t.Errorf("empty input: got %v, %v", convs, err)
	}
	// Header-only output (inbox zero) decodes to no rows.
	convs, err = DecodeConversations("ChannelID,ChannelName,ChannelType,UnreadCount,LastRead,Latest\n")
	if err != nil || len(convs) != 0 {
		t.Errorf("header-only input: got %v, %v", convs, err)
	}
}

func TestRedact(t *testing.T) {
	in := "auth failed for xoxp-123-abc-DEF and cookie xoxd-foo.bar"
	out := redact(in)
	if want := "auth failed for xox?-[REDACTED] and cookie xox?-[REDACTED]"; out != want {
		t.Errorf("redact = %q, want %q", out, want)
	}
}
