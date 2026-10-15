package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic calls the Claude Messages API through the official SDK with
// strict client tools.
type Anthropic struct {
	client anthropic.Client
	model  string
	extra  []option.RequestOption
}

// fallbackModels accept the server-side refusal fallback ("fallbacks":
// "default"), which re-serves a declined request on another model.
var fallbackModels = map[string]bool{
	"claude-fable-5-1": true, "claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true,
}

// NewAnthropic builds a Claude backend. baseURL may be empty. workspaceID is
// needed only for API keys that are not scoped to a workspace. timeout bounds
// one model call.
func NewAnthropic(apiKey, model, baseURL, workspaceID string, timeout time.Duration) *Anthropic {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithRequestTimeout(timeout),
		option.WithMaxRetries(1),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if workspaceID != "" {
		opts = append(opts, option.WithHeader("anthropic-workspace-id", workspaceID))
	}
	a := &Anthropic{client: anthropic.NewClient(opts...), model: model}
	if baseURL == "" && fallbackModels[model] {
		a.extra = []option.RequestOption{
			option.WithHeaderAdd("anthropic-beta", string(anthropic.AnthropicBetaServerSideFallback2026_07_01)),
			option.WithJSONSet("fallbacks", "default"),
		}
	}
	return a
}

func anthropicTools(specs []ToolSpec) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, len(specs))
	for i, s := range specs {
		schema := anthropic.ToolInputSchemaParam{
			Properties:  s.Schema["properties"],
			ExtraFields: map[string]any{"additionalProperties": false},
		}
		if req, ok := s.Schema["required"].([]string); ok {
			schema.Required = req
		}
		t := anthropic.ToolParam{
			Name:        s.Name,
			Description: anthropic.String(s.Description),
			InputSchema: schema,
			Strict:      anthropic.Bool(true),
		}
		out[i] = anthropic.ToolUnionParam{OfTool: &t}
	}
	return out
}

// Start implements Model.
func (a *Anthropic) Start(req Request) Session {
	var msgs []anthropic.MessageParam
	for _, h := range req.History {
		block := anthropic.NewTextBlock(h.Text)
		if h.Role == RoleAssistant {
			msgs = append(msgs, anthropic.NewAssistantMessage(block))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(block))
		}
	}
	msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(UserContent(req))))
	return &anthropicSession{a: a, params: anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 8192,
		// The system prompt and tool list are fixed, so tools+system form a
		// stable, cacheable prefix.
		System:   []anthropic.TextBlockParam{{Text: req.System, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		Tools:    anthropicTools(req.Tools),
		Messages: msgs,
		OutputConfig: anthropic.OutputConfigParam{
			// Expense bookkeeping is simple tool use; low effort keeps
			// latency and cost down.
			Effort: anthropic.OutputConfigEffortLow,
		},
	}}
}

type anthropicSession struct {
	a      *Anthropic
	params anthropic.MessageNewParams
}

func (s *anthropicSession) Next(ctx context.Context, results []ToolResult) (Turn, error) {
	if len(results) > 0 {
		// All results of one turn go back in a single user message.
		blocks := make([]anthropic.ContentBlockParamUnion, len(results))
		for i, r := range results {
			blocks[i] = anthropic.NewToolResultBlock(r.CallID, r.Content, r.IsError)
		}
		s.params.Messages = append(s.params.Messages, anthropic.NewUserMessage(blocks...))
	}
	resp, err := s.a.client.Messages.New(ctx, s.params, s.a.extra...)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			// Never include the request/response dump: it can contain the
			// user's messages. The status code is enough to diagnose.
			return Turn{}, fmt.Errorf("%w: anthropic API status %d", ErrUnavailable, apiErr.StatusCode)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return Turn{}, fmt.Errorf("%w: %w", ErrUnavailable, context.DeadlineExceeded)
		}
		return Turn{}, fmt.Errorf("%w: anthropic request failed", ErrUnavailable)
	}
	turn := Turn{Usage: Usage{
		InputTokens:     resp.Usage.InputTokens + resp.Usage.CacheCreationInputTokens + resp.Usage.CacheReadInputTokens,
		OutputTokens:    resp.Usage.OutputTokens,
		CacheReadTokens: resp.Usage.CacheReadInputTokens,
	}}
	switch resp.StopReason {
	case anthropic.StopReasonRefusal:
		return turn, fmt.Errorf("%w: model declined the request", ErrUnavailable)
	case anthropic.StopReasonMaxTokens:
		return turn, fmt.Errorf("%w: answer truncated", ErrMalformed)
	}
	// Append the answer verbatim (thinking blocks included) before running
	// any tool: the transcript stays append-only.
	s.params.Messages = append(s.params.Messages, resp.ToParam())

	var text strings.Builder
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			text.WriteString(b.Text)
		case anthropic.ToolUseBlock:
			turn.Calls = append(turn.Calls, ToolCall{ID: b.ID, Name: b.Name, Input: []byte(b.JSON.Input.Raw())})
		}
	}
	turn.Text = strings.TrimSpace(text.String())
	return turn, nil
}
