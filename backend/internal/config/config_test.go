package config

import (
	"strings"
	"testing"
)

func TestParseMembers(t *testing.T) {
	m, err := ParseMembers(" 111:Мама , 222:Папа ")
	if err != nil || len(m) != 2 || m[0].ID != 111 || m[1].Name != "Папа" {
		t.Fatalf("got %+v, %v", m, err)
	}
	for _, bad := range []string{"", "abc:Mom", "111", "111:", "-5:X", "1:A,1:B"} {
		if _, err := ParseMembers(bad); err == nil {
			t.Errorf("ParseMembers(%q) should fail", bad)
		}
	}
}

func TestLoadBotFailsFastWithoutSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("FAMILY_MEMBERS", "1:A")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("LLM_PROVIDER", "anthropic")
	t.Setenv("LLM_API_KEY", "")
	_, err := LoadBot()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"TELEGRAM_BOT_TOKEN", "LLM_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %s", err, want)
		}
	}
}

func TestLoadBotMockNeedsNoKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("FAMILY_MEMBERS", "1:A")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("LLM_PROVIDER", "mock")
	if _, err := LoadBot(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAPIRejectsWildcardCORS(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("FAMILY_MEMBERS", "1:A")
	t.Setenv("CORS_ALLOWED_ORIGINS", "*")
	if _, err := LoadAPI(); err == nil || !strings.Contains(err.Error(), "CORS") {
		t.Fatalf("expected CORS error, got %v", err)
	}
}
