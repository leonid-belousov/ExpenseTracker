package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAICompatible calls any server implementing the OpenAI Chat Completions
// API with function calling (OpenAI, OpenRouter, vLLM, Ollama, LM Studio, ...).
type OpenAICompatible struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewOpenAICompatible builds a backend. baseURL is e.g. https://api.openai.com/v1.
// timeout bounds one model call.
func NewOpenAICompatible(baseURL, apiKey, model string, timeout time.Duration) *OpenAICompatible {
	return &OpenAICompatible{baseURL: baseURL, apiKey: apiKey, model: model, http: &http.Client{Timeout: timeout}}
}

type oaFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

type oaTool struct {
	Type     string     `json:"type"`
	Function oaFunction `json:"function"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaRequest struct {
	Model       string            `json:"model"`
	Messages    []json.RawMessage `json:"messages"`
	Tools       []oaTool          `json:"tools"`
	ToolChoice  string            `json:"tool_choice"`
	Temperature float64           `json:"temperature"`
}

type oaResponse struct {
	Choices []struct {
		Message      json.RawMessage `json:"message"`
		FinishReason string          `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func rawMsg(m oaMessage) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

func strp(s string) *string { return &s }

// Start implements Model.
func (o *OpenAICompatible) Start(req Request) Session {
	msgs := []json.RawMessage{rawMsg(oaMessage{Role: "system", Content: strp(req.System)})}
	for _, h := range req.History {
		msgs = append(msgs, rawMsg(oaMessage{Role: string(h.Role), Content: strp(h.Text)}))
	}
	msgs = append(msgs, rawMsg(oaMessage{Role: "user", Content: strp(UserContent(req))}))
	tools := make([]oaTool, len(req.Tools))
	for i, t := range req.Tools {
		tools[i] = oaTool{Type: "function", Function: oaFunction{Name: t.Name, Description: t.Description, Parameters: t.Schema, Strict: true}}
	}
	return &openAISession{o: o, req: oaRequest{Model: o.model, Messages: msgs, Tools: tools, ToolChoice: "auto"}}
}

type openAISession struct {
	o   *OpenAICompatible
	req oaRequest
}

func (s *openAISession) Next(ctx context.Context, results []ToolResult) (Turn, error) {
	for _, r := range results {
		s.req.Messages = append(s.req.Messages, rawMsg(oaMessage{Role: "tool", ToolCallID: r.CallID, Content: strp(r.Content)}))
	}
	body, err := json.Marshal(s.req)
	if err != nil {
		return Turn{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.o.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Turn{}, fmt.Errorf("%w: invalid LLM_BASE_URL", ErrUnavailable)
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+s.o.apiKey)
	resp, err := s.o.http.Do(hreq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return Turn{}, fmt.Errorf("%w: %w", ErrUnavailable, context.DeadlineExceeded)
		}
		// Do not wrap err: keep logs free of request details.
		return Turn{}, fmt.Errorf("%w: LLM request failed", ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Turn{}, fmt.Errorf("%w: reading LLM response failed", ErrUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		return Turn{}, fmt.Errorf("%w: LLM API status %d", ErrUnavailable, resp.StatusCode)
	}
	var cr oaResponse
	if err := json.Unmarshal(data, &cr); err != nil || len(cr.Choices) == 0 {
		return Turn{}, fmt.Errorf("%w: unexpected response shape", ErrMalformed)
	}
	turn := Turn{Usage: Usage{InputTokens: cr.Usage.PromptTokens, OutputTokens: cr.Usage.CompletionTokens}}
	if cr.Choices[0].FinishReason == "length" {
		return turn, fmt.Errorf("%w: answer truncated", ErrMalformed)
	}
	var m oaMessage
	if err := json.Unmarshal(cr.Choices[0].Message, &m); err != nil {
		return turn, fmt.Errorf("%w: unexpected message shape", ErrMalformed)
	}
	// Append the assistant message exactly as received.
	s.req.Messages = append(s.req.Messages, cr.Choices[0].Message)
	if m.Content != nil {
		turn.Text = *m.Content
	}
	for _, c := range m.ToolCalls {
		turn.Calls = append(turn.Calls, ToolCall{ID: c.ID, Name: c.Function.Name, Input: json.RawMessage(c.Function.Arguments)})
	}
	return turn, nil
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}
