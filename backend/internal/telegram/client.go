// Package telegram is a minimal Bot API client (long polling + sendMessage).
// The bot token is part of every request URL, so errors are always scrubbed
// before they can reach a log.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// User is the sender of a message.
type User struct {
	ID    int64 `json:"id"`
	IsBot bool  `json:"is_bot"`
}

// Chat is the conversation a message belongs to.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // private, group, supergroup, channel
}

// Message is an incoming message.
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

// Update is one Bot API update.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// Sender sends replies; implemented by Client and by test fakes.
type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// Client talks to the Telegram Bot API.
type Client struct {
	base  string // https://api.telegram.org/bot<token>
	token string
	http  *http.Client
}

// NewClient builds a client. apiURL is normally https://api.telegram.org.
func NewClient(apiURL, token string) *Client {
	return &Client{base: apiURL + "/bot" + token, token: token, http: &http.Client{Timeout: 70 * time.Second}}
}

// APIError is a non-OK Bot API answer.
type APIError struct {
	Method      string
	Code        int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s failed: %d %s", e.Method, e.Code, e.Description)
}

func (c *Client) scrub(err error) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		// url.Error embeds the full URL including the token.
		inner := ue.Err
		if inner != nil {
			return fmt.Errorf("telegram request %s: %s", ue.Op, strings.ReplaceAll(inner.Error(), c.token, "<redacted>"))
		}
		return errors.New("telegram request failed")
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.token, "<redacted>"))
}

func (c *Client) call(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.scrub(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return c.scrub(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return c.scrub(err)
	}
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("telegram %s: invalid response (HTTP %d)", method, resp.StatusCode)
	}
	if !env.OK {
		return &APIError{Method: method, Code: env.ErrorCode, Description: env.Description}
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// GetUpdates long-polls for message updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var ups []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message"},
	}, &ups)
	return ups, err
}

// SendMessage sends a plain-text message (no parse mode, so user-provided
// text cannot inject markup).
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	return c.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text}, nil)
}

// GetMe validates the token.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, "getMe", map[string]any{}, &u)
	return u, err
}
