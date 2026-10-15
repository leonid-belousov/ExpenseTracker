// Package config loads and validates settings from environment variables.
// Each subcommand validates only what it needs and fails fast with a clear,
// secret-free message.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embed the zone database so APP_TIMEZONE works in minimal images

	"expensetracker/internal/expense"
)

// Common settings used by every subcommand.
type Common struct {
	DatabaseURL string
	Location    *time.Location
	Currency    string
	Members     []expense.Member
	LogLevel    string
}

// API settings.
type API struct {
	Common
	Addr           string
	AllowedOrigins []string
}

// LLM settings.
type LLM struct {
	Provider string // anthropic | openai | mock
	APIKey   string
	Model    string
	BaseURL  string
	Timeout  time.Duration
}

// Bot settings.
type Bot struct {
	Common
	TelegramToken  string
	TelegramAPIURL string
	Language       string
	LLM            LLM
	HeartbeatFile  string
}

type errs []string

func (e *errs) add(format string, a ...any) { *e = append(*e, fmt.Sprintf(format, a...)) }
func (e errs) err() error {
	if len(e) == 0 {
		return nil
	}
	return errors.New("invalid configuration:\n  - " + strings.Join(e, "\n  - "))
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

func loadCommon(e *errs, needMembers bool) Common {
	c := Common{
		DatabaseURL: env("DATABASE_URL", ""),
		Currency:    env("CURRENCY", "RUB"),
		LogLevel:    env("LOG_LEVEL", "info"),
	}
	if c.DatabaseURL == "" {
		e.add("DATABASE_URL is required")
	}
	tz := env("APP_TIMEZONE", "Europe/Moscow")
	loc, err := time.LoadLocation(tz)
	if err != nil {
		e.add("APP_TIMEZONE %q is not a valid IANA time zone", tz)
		loc = time.UTC
	}
	c.Location = loc
	if !currencyRe.MatchString(c.Currency) {
		e.add("CURRENCY must be a 3-letter ISO 4217 code such as RUB")
	}
	if needMembers {
		members, err := ParseMembers(env("FAMILY_MEMBERS", ""))
		if err != nil {
			e.add("FAMILY_MEMBERS: %v", err)
		}
		c.Members = members
	}
	return c
}

// ParseMembers parses "123456789:Alice,987654321:Bob". The ids are Telegram
// user ids and also form the bot allowlist.
func ParseMembers(s string) ([]expense.Member, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("at least one member is required, format: <telegram_id>:<name>,<telegram_id>:<name>")
	}
	var out []expense.Member
	seen := map[int64]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idStr, name, ok := strings.Cut(part, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("entry %q must look like <telegram_id>:<name>", part)
		}
		id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("entry %q has an invalid Telegram user id", part)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate Telegram user id %d", id)
		}
		seen[id] = true
		out = append(out, expense.Member{ID: id, Name: name})
	}
	if len(out) == 0 {
		return nil, errors.New("at least one member is required")
	}
	return out, nil
}

// LoadMigrate loads settings for the migrate subcommand.
func LoadMigrate() (Common, error) {
	var e errs
	c := loadCommon(&e, false)
	return c, e.err()
}

// LoadAPI loads settings for the REST API.
func LoadAPI() (API, error) {
	var e errs
	c := API{Common: loadCommon(&e, true), Addr: env("HTTP_ADDR", ":8080")}
	for _, o := range strings.Split(env("CORS_ALLOWED_ORIGINS", "http://127.0.0.1:8080,http://localhost:8080"), ",") {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		if o == "" {
			continue
		}
		if o == "*" {
			e.add("CORS_ALLOWED_ORIGINS must list explicit origins; '*' is not allowed")
			continue
		}
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			e.add("CORS_ALLOWED_ORIGINS entry %q must be an origin like http://127.0.0.1:8080", o)
			continue
		}
		c.AllowedOrigins = append(c.AllowedOrigins, o)
	}
	return c, e.err()
}

// LoadBot loads settings for the Telegram bot.
func LoadBot() (Bot, error) {
	var e errs
	c := Bot{
		Common:         loadCommon(&e, true),
		TelegramToken:  env("TELEGRAM_BOT_TOKEN", ""),
		TelegramAPIURL: strings.TrimRight(env("TELEGRAM_API_URL", "https://api.telegram.org"), "/"),
		Language:       env("BOT_LANGUAGE", "ru"),
		HeartbeatFile:  env("BOT_HEARTBEAT_FILE", "/tmp/expense-bot-heartbeat"),
	}
	if c.TelegramToken == "" {
		e.add("TELEGRAM_BOT_TOKEN is required (create a bot with @BotFather)")
	}
	if c.Language != "ru" && c.Language != "en" {
		e.add("BOT_LANGUAGE must be 'ru' or 'en'")
	}
	c.LLM = loadLLM(&e)
	return c, e.err()
}

func loadLLM(e *errs) LLM {
	l := LLM{
		Provider: strings.ToLower(env("LLM_PROVIDER", "")),
		APIKey:   env("LLM_API_KEY", ""),
		Model:    env("LLM_MODEL", ""),
		BaseURL:  strings.TrimRight(env("LLM_BASE_URL", ""), "/"),
	}
	timeout, err := time.ParseDuration(env("LLM_TIMEOUT", "30s"))
	if err != nil || timeout <= 0 || timeout > 5*time.Minute {
		e.add("LLM_TIMEOUT must be a duration between 1s and 5m, e.g. 30s")
		timeout = 30 * time.Second
	}
	l.Timeout = timeout
	switch l.Provider {
	case "anthropic":
		if l.Model == "" {
			l.Model = "claude-opus-5-5"
		}
		if l.APIKey == "" {
			e.add("LLM_API_KEY is required for LLM_PROVIDER=anthropic")
		}
	case "openai":
		if l.APIKey == "" {
			e.add("LLM_API_KEY is required for LLM_PROVIDER=openai (use any non-empty value for keyless local servers)")
		}
		if l.Model == "" {
			e.add("LLM_MODEL is required for LLM_PROVIDER=openai")
		}
		if l.BaseURL == "" {
			l.BaseURL = "https://api.openai.com/v1"
		}
	case "mock":
		// Rule-based local development parser; no credentials needed.
	case "":
		e.add("LLM_PROVIDER is required: anthropic, openai or mock (mock is for local development only)")
	default:
		e.add("LLM_PROVIDER must be anthropic, openai or mock")
	}
	if l.BaseURL != "" {
		if u, err := url.Parse(l.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			e.add("LLM_BASE_URL must be an http(s) URL")
		}
	}
	return l
}
