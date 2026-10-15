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
// API (OpenAI, OpenRouter, vLLM, Ollama, LM Studio, ...). It requests a JSON
// object answer; the result is validated by Decode like every other backend.
type OpenAICompatible struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewOpenAICompatible builds a backend. baseURL is e.g. https://api.openai.com/v1.
func NewOpenAICompatible(baseURL, apiKey, model string, timeout time.Duration) *OpenAICompatible {
	return &OpenAICompatible{baseURL: baseURL, apiKey: apiKey, model: model, http: &http.Client{Timeout: timeout}}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string            `json:"model"`
	Messages       []chatMessage     `json:"messages"`
	Temperature    float64           `json:"temperature"`
	ResponseFormat map[string]string `json:"response_format"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

// Complete implements Completer.
func (o *OpenAICompatible) Complete(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:          o.model,
		Messages:       []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		Temperature:    0,
		ResponseFormat: map[string]string{"type": "json_object"},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("invalid LLM_BASE_URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := o.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return "", context.DeadlineExceeded
		}
		// Do not wrap err: *url.Error prints the URL, which is fine, but keep
		// logs uniform and free of request details.
		return "", errors.New("LLM request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", errors.New("reading LLM response failed")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM API status %d", resp.StatusCode)
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil || len(cr.Choices) == 0 {
		return "", fmt.Errorf("%w: unexpected response shape", ErrMalformed)
	}
	if cr.Choices[0].FinishReason == "length" {
		return "", fmt.Errorf("%w: answer truncated", ErrMalformed)
	}
	return cr.Choices[0].Message.Content, nil
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}
