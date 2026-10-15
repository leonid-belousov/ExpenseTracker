package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testTools = []ToolSpec{{
	Name: "find", Description: "find things",
	Schema: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}},
		"required": []string{"q"}, "additionalProperties": false},
}}

func testRequest() Request {
	return Request{
		System: "sys", Tools: testTools, Context: "Context: today", Text: "удали такси",
		History: []HistoryMessage{{Role: RoleUser, Text: "кофе 350"}, {Role: RoleAssistant, Text: "Записал"}},
	}
}

func TestOpenAICompatibleToolLoop(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"find","arguments":"{\"q\":\"такс\"}"}}]}}],
				"usage":{"prompt_tokens":100,"completion_tokens":20}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Удалил"}}],"usage":{"prompt_tokens":150,"completion_tokens":5}}`)
	}))
	defer srv.Close()

	s := NewOpenAICompatible(srv.URL+"/v1", "k", "m", time.Second).Start(testRequest())
	turn, err := s.Next(context.Background(), nil)
	if err != nil || len(turn.Calls) != 1 || turn.Calls[0].Name != "find" || string(turn.Calls[0].Input) != `{"q":"такс"}` || turn.Usage.InputTokens != 100 {
		t.Fatalf("turn %+v %v", turn, err)
	}
	turn, err = s.Next(context.Background(), []ToolResult{{CallID: "call_1", Content: `{"ok":true}`}})
	if err != nil || turn.Text != "Удалил" || len(turn.Calls) != 0 {
		t.Fatalf("turn %+v %v", turn, err)
	}
	msgs := bodies[1]["messages"].([]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,user,assistant,tool" {
		t.Fatalf("roles %v", roles)
	}
	if !strings.Contains(msgs[3].(map[string]any)["content"].(string), "User message:\nудали такси") {
		t.Fatalf("user message %v", msgs[3])
	}
	if msgs[5].(map[string]any)["tool_call_id"] != "call_1" {
		t.Fatalf("tool message %v", msgs[5])
	}
	fn := bodies[0]["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if fn["strict"] != true || fn["name"] != "find" {
		t.Fatalf("tool %v", fn)
	}
}

func TestOpenAICompatibleErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   error
	}{
		"status":    {503, `{}`, ErrUnavailable},
		"shape":     {200, `{"choices":[]}`, ErrMalformed},
		"truncated": {200, `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"..."}}]}`, ErrMalformed},
	}
	for name, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		_, err := NewOpenAICompatible(srv.URL, "k", "m", time.Second).Start(testRequest()).Next(context.Background(), nil)
		srv.Close()
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err %v", name, err)
		}
	}
}

func TestAnthropicToolLoop(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"tool_use",
				"content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"tool_use","id":"tu_1","name":"find","input":{"q":"такс"}}],
				"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":50,"cache_creation_input_tokens":0}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"msg_2","type":"message","role":"assistant","model":"m","stop_reason":"end_turn",
			"content":[{"type":"text","text":"Удалил"}],"usage":{"input_tokens":10,"output_tokens":5}}`)
	}))
	defer srv.Close()

	s := NewAnthropic("k", "claude-opus-5-5", srv.URL, time.Second).Start(testRequest())
	turn, err := s.Next(context.Background(), nil)
	if err != nil || len(turn.Calls) != 1 || turn.Calls[0].ID != "tu_1" || turn.Usage.InputTokens != 150 || turn.Usage.CacheReadTokens != 50 {
		t.Fatalf("turn %+v %v", turn, err)
	}
	var in map[string]string
	if err := json.Unmarshal(turn.Calls[0].Input, &in); err != nil || in["q"] != "такс" {
		t.Fatalf("input %s", turn.Calls[0].Input)
	}
	turn, err = s.Next(context.Background(), []ToolResult{{CallID: "tu_1", Content: `{"ok":true}`}})
	if err != nil || turn.Text != "Удалил" {
		t.Fatalf("turn %+v %v", turn, err)
	}
	// The second request replays the first answer verbatim (thinking block
	// included) and then the tool result: the transcript is append-only.
	msgs := bodies[1]["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("messages %d", len(msgs))
	}
	prev := msgs[3].(map[string]any)["content"].([]any)
	if prev[0].(map[string]any)["type"] != "thinking" || prev[0].(map[string]any)["signature"] != "sig" {
		t.Fatalf("assistant turn not replayed verbatim: %v", prev)
	}
	result := msgs[4].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "tu_1" {
		t.Fatalf("tool result %v", result)
	}
	tool := bodies[0]["tools"].([]any)[0].(map[string]any)
	if tool["strict"] != true || tool["input_schema"].(map[string]any)["additionalProperties"] != false {
		t.Fatalf("tool %v", tool)
	}
	// A custom base URL (proxy) gets no beta-only fields.
	if _, ok := bodies[0]["fallbacks"]; ok {
		t.Fatal("fallbacks sent to a custom base URL")
	}
}

func TestAnthropicRefusalAndStatus(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
	}{
		"refusal": {200, `{"id":"m","type":"message","role":"assistant","model":"m","stop_reason":"refusal","content":[],"usage":{"input_tokens":1,"output_tokens":0}}`},
		"status":  {400, `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		_, err := NewAnthropic("k", "m", srv.URL, time.Second).Start(testRequest()).Next(context.Background(), nil)
		srv.Close()
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: err %v", name, err)
		}
	}
}
