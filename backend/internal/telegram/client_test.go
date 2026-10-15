package telegram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const token = "123456:SECRET-token"

func TestErrorsNeverContainToken(t *testing.T) {
	// Unreachable host: net/http's *url.Error would normally print the URL.
	c := NewClient("http://127.0.0.1:1", token)
	err := c.SendMessage(context.Background(), 1, "hi")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error leaks token or is nil: %v", err)
	}
}

func TestAPIErrorAndSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
			t.Errorf("path %s", r.URL.Path)
		}
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":5,"message":{"message_id":1,"from":{"id":7},"chat":{"id":7,"type":"private"},"text":"кофе 350"}}]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, token)
	_, err := c.GetMe(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 401 {
		t.Fatalf("getMe: %v", err)
	}
	ups, err := c.GetUpdates(context.Background(), 0, 0)
	if err != nil || len(ups) != 1 || ups[0].Message.From.ID != 7 || ups[0].Message.Text != "кофе 350" {
		t.Fatalf("updates %+v %v", ups, err)
	}
}
