// Package llm is a provider-neutral interface to tool-calling language
// models, with adapters for the Anthropic Messages API and OpenAI-compatible
// Chat Completions APIs.
//
// A Session is one agent turn: the provider keeps its own native transcript
// and only ever appends to it (assistant answers are sent back verbatim, which
// keeps Anthropic thinking blocks valid and the prompt cache warm). Earlier
// turns are passed in as plain text History, never as replayed provider
// blocks.
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// ToolSpec describes one tool. Schema is a JSON Schema object whose
// properties are all required and additionalProperties is false, so it can be
// used with strict tool use on every provider.
type ToolSpec struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ToolCall is a model's request to run a tool.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult answers one ToolCall.
type ToolResult struct {
	CallID  string
	Content string // JSON text
	IsError bool
}

// Usage counts tokens for one model call.
type Usage struct {
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
}

// Add accumulates u2 into u.
func (u *Usage) Add(u2 Usage) {
	u.InputTokens += u2.InputTokens
	u.OutputTokens += u2.OutputTokens
	u.CacheReadTokens += u2.CacheReadTokens
}

// Turn is one assistant answer: optional text and zero or more tool calls.
// No tool calls means the model has finished.
type Turn struct {
	Text  string
	Calls []ToolCall
	Usage Usage
}

// Role of a history message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// HistoryMessage is an earlier message of the conversation, as plain text.
type HistoryMessage struct {
	Role Role
	Text string
}

// Request starts a session.
type Request struct {
	System  string // fixed instructions (cacheable: no per-request data)
	Tools   []ToolSpec
	History []HistoryMessage // alternating, starting with a user message
	// Context is per-request data (today's date, time zone, ...) placed in
	// the final user message, after the cacheable prefix.
	Context string
	Text    string // the user's new message (untrusted)
}

// Session is one agent turn.
type Session interface {
	// Next sends the results of the previous turn's tool calls (nil on the
	// first call) and returns the model's next turn.
	Next(ctx context.Context, results []ToolResult) (Turn, error)
}

// Model starts sessions.
type Model interface {
	Start(req Request) Session
}

// Errors returned by sessions.
var (
	// ErrUnavailable: the provider timed out, failed, or refused.
	ErrUnavailable = errors.New("language model is unavailable")
	// ErrMalformed: the provider answered but not with a usable turn.
	ErrMalformed = errors.New("language model returned an invalid answer")
)

// UserContent renders the final user message from the request context and
// the user's text.
func UserContent(req Request) string {
	if req.Context == "" {
		return req.Text
	}
	return req.Context + "\n\nUser message:\n" + req.Text
}
