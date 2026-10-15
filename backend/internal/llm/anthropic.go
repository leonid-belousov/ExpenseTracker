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

// Anthropic calls the Claude Messages API through the official SDK and asks
// for schema-constrained JSON output.
type Anthropic struct {
	client anthropic.Client
	model  string
}

// NewAnthropic builds a Claude backend. baseURL may be empty.
func NewAnthropic(apiKey, model, baseURL string, timeout time.Duration) *Anthropic {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithRequestTimeout(timeout),
		option.WithMaxRetries(1),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Anthropic{client: anthropic.NewClient(opts...), model: model}
}

// Complete implements Completer.
func (a *Anthropic) Complete(ctx context.Context, system, user string) (string, error) {
	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
		OutputConfig: anthropic.OutputConfigParam{
			// Parsing a short message is a simple task; low effort keeps
			// latency and cost down.
			Effort: anthropic.OutputConfigEffortLow,
			Format: anthropic.JSONOutputFormatParam{Schema: IntentSchema()},
		},
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			// Never include the request/response dump: it can contain the
			// user's message. The status code is enough to diagnose.
			return "", fmt.Errorf("anthropic API status %d", apiErr.StatusCode)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return "", context.DeadlineExceeded
		}
		return "", errors.New("anthropic request failed")
	}
	switch resp.StopReason {
	case anthropic.StopReasonRefusal:
		return "", errors.New("model declined the request")
	case anthropic.StopReasonMaxTokens:
		return "", fmt.Errorf("%w: answer truncated", ErrMalformed)
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), nil
}
