// Package mcp is a thin client over slack-mcp-server. It spawns the server as
// a child process, speaks MCP over stdio, exposes a small CallTool surface, and
// decodes the server's CSV responses into domain types.
package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/genkio/tui/plugins/slack/internal/config"
)

// Tool names exposed by slack-mcp-server that this client uses.
const (
	ToolUnreads        = "conversations_unreads"
	ToolHistory        = "conversations_history"
	ToolReplies        = "conversations_replies"
	ToolMark           = "conversations_mark"
	ToolChannels       = "channels_list"
	ToolReactionAdd    = "reactions_add"
	ToolReactionRemove = "reactions_remove"
)

// Client is a connected MCP session to a single slack-mcp-server process.
type Client struct {
	session *sdk.ClientSession
	stderr  *stderrBuffer
	tools   map[string]struct{}

	mu sync.Mutex // serializes tool calls over the single stdio session
}

// Connect launches the server described by sc and completes the MCP handshake.
// The caller must Close the returned client to terminate the child process.
func Connect(ctx context.Context, sc config.ServerConfig) (*Client, error) {
	cmd := exec.Command(sc.Command, sc.Args...) // inherits env, forwarding SLACK_MCP_* to the server
	stderr := newStderrBuffer()
	cmd.Stderr = stderr

	impl := &sdk.Implementation{Name: "slack-tui", Version: "0.1.0"}
	transport := &sdk.CommandTransport{Command: cmd}

	session, err := sdk.NewClient(impl, nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("starting MCP server %q: %w%s", sc.Command, err, stderr.hint())
	}

	c := &Client{session: session, stderr: stderr, tools: map[string]struct{}{}}
	if err := c.loadTools(ctx); err != nil {
		session.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) loadTools(ctx context.Context) error {
	params := &sdk.ListToolsParams{}
	for {
		res, err := c.session.ListTools(ctx, params)
		if err != nil {
			return fmt.Errorf("listing tools: %w%s", err, c.stderr.hint())
		}
		for _, t := range res.Tools {
			c.tools[t.Name] = struct{}{}
		}
		if res.NextCursor == "" {
			break
		}
		params.Cursor = res.NextCursor
	}
	return nil
}

func (c *Client) HasTool(name string) bool {
	_, ok := c.tools[name]
	return ok
}

func (c *Client) ToolNames() []string {
	names := make([]string, 0, len(c.tools))
	for name := range c.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CallTool invokes a tool and returns its concatenated text content. The
// server returns CSV (for listings) or a plain confirmation string (for
// actions) in a single text block.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	res, err := c.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("calling %s: %w%s", name, err, c.stderr.hint())
	}
	text := textOf(res)
	if res.IsError {
		return "", fmt.Errorf("%s failed: %s", name, strings.TrimSpace(redact(text)))
	}
	return text, nil
}

// Close shuts down the session, which terminates the child server process.
func (c *Client) Close() error {
	if c == nil || c.session == nil {
		return nil
	}
	return c.session.Close()
}

func textOf(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// stderrBuffer keeps a bounded tail of the server's stderr so we can attach it
// to error messages for diagnosis. It is never shown in the main UI.
type stderrBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const stderrMax = 8 << 10

func newStderrBuffer() *stderrBuffer { return &stderrBuffer{} }

func (s *stderrBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	if len(s.buf) > stderrMax {
		s.buf = s.buf[len(s.buf)-stderrMax:]
	}
	return len(p), nil
}

// hint returns the redacted stderr tail formatted for appending to an error,
// or an empty string if the server printed nothing.
func (s *stderrBuffer) hint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := strings.TrimSpace(redact(string(s.buf)))
	if out == "" {
		return ""
	}
	return "\nserver output:\n" + out
}

// tokenRE matches any Slack token/cookie shape so values never reach a log or
// an error message. It is deliberately greedy to the next whitespace because
// xoxd cookies contain punctuation we still must not leak.
var tokenRE = regexp.MustCompile(`xox[a-z]-\S+`)

func redact(s string) string { return tokenRE.ReplaceAllString(s, "xox?-[REDACTED]") }
