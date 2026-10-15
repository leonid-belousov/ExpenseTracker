// Package llm turns a free-text Telegram message into a constrained,
// validated Intent. The model is treated as an untrusted parser: its output is
// decoded into a fixed schema, every field is validated in Go, and any field
// outside the schema (for example a user id) is ignored. The model never
// executes queries or chooses whose data is touched.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/money"
)

// Action is the operation the user asked for.
type Action string

const (
	ActionCreate  Action = "create"
	ActionList    Action = "list"
	ActionSummary Action = "summary"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionClarify Action = "clarify"
	ActionHelp    Action = "help"
)

var actions = []Action{ActionCreate, ActionList, ActionSummary, ActionUpdate, ActionDelete, ActionClarify, ActionHelp}

// Target identifies an existing expense of the current user.
type Target struct {
	Last     bool // "the last one" = most recently recorded
	Amount   *money.Amount
	Category *expense.Category
	Text     string
	Date     *dates.Date
}

// HasCriteria reports whether any matching criterion is present.
func (t Target) HasCriteria() bool {
	return t.Last || t.Amount != nil || t.Category != nil || t.Text != "" || t.Date != nil
}

// Changes lists fields to modify in an update.
type Changes struct {
	Amount   *money.Amount
	Category *expense.Category
	Date     *dates.Date
	Comment  *string
}

// Empty reports whether no change was requested.
func (c Changes) Empty() bool {
	return c.Amount == nil && c.Category == nil && c.Date == nil && c.Comment == nil
}

// Intent is a validated user request.
type Intent struct {
	Action   Action
	Amount   *money.Amount
	Category *expense.Category
	Date     *dates.Date
	Comment  *string
	// Period is a half-open range; nil means "use the default period".
	Period   *dates.Range
	Target   Target
	Changes  Changes
	Question string
}

// Request is the input to a Parser.
type Request struct {
	Text     string
	Today    dates.Date
	TimeZone string
	Language string
}

// Parser converts text into an Intent.
type Parser interface {
	Parse(ctx context.Context, req Request) (Intent, error)
}

// Completer is a raw LLM backend that returns the model's text answer.
type Completer interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// Errors returned by Parse.
var (
	// ErrUnavailable: the provider timed out, failed, or refused.
	ErrUnavailable = errors.New("language model is unavailable")
	// ErrMalformed: the provider answered but not with a valid intent.
	ErrMalformed = errors.New("language model returned an invalid answer")
)

// FieldError reports an invalid value the user (via the model) supplied,
// e.g. a negative amount. The bot explains it instead of guessing.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

// MaxInputRunes bounds the text sent to the model.
const MaxInputRunes = 1000

// ModelParser builds an Intent through a Completer and strict validation.
type ModelParser struct {
	Backend Completer
}

// Parse implements Parser.
func (p ModelParser) Parse(ctx context.Context, req Request) (Intent, error) {
	text := strings.TrimSpace(req.Text)
	if utf8.RuneCountInString(text) > MaxInputRunes {
		text = string([]rune(text)[:MaxInputRunes])
	}
	raw, err := p.Backend.Complete(ctx, SystemPrompt(), UserPrompt(req, text))
	if err != nil {
		if errors.Is(err, ErrMalformed) {
			return Intent{}, err
		}
		return Intent{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return Decode(raw, req.Today)
}

// ---- raw schema ----

type rawPeriod struct {
	Preset *string `json:"preset"`
	From   *string `json:"from"`
	To     *string `json:"to"`
}

type rawTarget struct {
	Last     *bool           `json:"last"`
	Amount   json.RawMessage `json:"amount"`
	Category *string         `json:"category"`
	Text     *string         `json:"text"`
	Date     *string         `json:"date"`
}

type rawChanges struct {
	Amount   json.RawMessage `json:"amount"`
	Category *string         `json:"category"`
	Date     *string         `json:"date"`
	Comment  *string         `json:"comment"`
}

type rawIntent struct {
	Action   string          `json:"action"`
	Amount   json.RawMessage `json:"amount"`
	Category *string         `json:"category"`
	Date     *string         `json:"date"`
	Comment  *string         `json:"comment"`
	Period   *rawPeriod      `json:"period"`
	Target   *rawTarget      `json:"target"`
	Changes  *rawChanges     `json:"changes"`
	Question *string         `json:"question"`
}

// extractJSON tolerates code fences or prose around a single JSON object.
func extractJSON(s string) (string, bool) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return "", false
	}
	return s[start : end+1], true
}

// Decode validates a model answer into an Intent. today is used to resolve
// relative dates and periods in the deployment time zone.
func Decode(answer string, today dates.Date) (Intent, error) {
	body, ok := extractJSON(answer)
	if !ok {
		return Intent{}, fmt.Errorf("%w: no JSON object", ErrMalformed)
	}
	var r rawIntent
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return Intent{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	in := Intent{Action: Action(strings.ToLower(strings.TrimSpace(r.Action)))}
	valid := false
	for _, a := range actions {
		if in.Action == a {
			valid = true
		}
	}
	if !valid {
		return Intent{}, fmt.Errorf("%w: unknown action", ErrMalformed)
	}

	var err error
	if in.Amount, err = decodeAmount("amount", r.Amount); err != nil {
		return Intent{}, err
	}
	in.Category = decodeCategory(r.Category)
	if in.Date, err = decodeDate("date", r.Date, today); err != nil {
		return Intent{}, err
	}
	in.Comment = clip(r.Comment, expense.MaxCommentLength)
	if r.Period != nil {
		if in.Period, err = decodePeriod(*r.Period, today); err != nil {
			return Intent{}, err
		}
	}
	if r.Target != nil {
		t := r.Target
		if t.Last != nil {
			in.Target.Last = *t.Last
		}
		if in.Target.Amount, err = decodeAmount("target.amount", t.Amount); err != nil {
			return Intent{}, err
		}
		in.Target.Category = decodeCategory(t.Category)
		if s := clip(t.Text, 100); s != nil {
			in.Target.Text = *s
		}
		if in.Target.Date, err = decodeDate("target.date", t.Date, today); err != nil {
			return Intent{}, err
		}
	}
	if r.Changes != nil {
		c := r.Changes
		if in.Changes.Amount, err = decodeAmount("changes.amount", c.Amount); err != nil {
			return Intent{}, err
		}
		in.Changes.Category = decodeCategory(c.Category)
		if in.Changes.Date, err = decodeDate("changes.date", c.Date, today); err != nil {
			return Intent{}, err
		}
		in.Changes.Comment = clip(c.Comment, expense.MaxCommentLength)
	}
	if q := clip(r.Question, 300); q != nil {
		in.Question = *q
	}
	return in, nil
}

func decodeAmount(field string, raw json.RawMessage) (*money.Amount, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var a money.Amount
	if err := a.UnmarshalJSON(raw); err != nil {
		return nil, &FieldError{Field: field, Err: err}
	}
	if err := a.ValidatePositive(); err != nil {
		return nil, &FieldError{Field: field, Err: err}
	}
	return &a, nil
}

// decodeCategory returns nil for missing or non-canonical values: an unknown
// category is treated as "unclear", never coerced.
func decodeCategory(s *string) *expense.Category {
	if s == nil {
		return nil
	}
	c, err := expense.ParseCategory(*s)
	if err != nil {
		return nil
	}
	return &c
}

func decodeDate(field string, s *string, today dates.Date) (*dates.Date, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	v := strings.ToLower(strings.TrimSpace(*s))
	var d dates.Date
	switch v {
	case "today":
		d = today
	case "yesterday":
		d = today.AddDays(-1)
	case "day_before_yesterday":
		d = today.AddDays(-2)
	default:
		p, err := dates.Parse(v)
		if err != nil {
			return nil, &FieldError{Field: field, Err: err}
		}
		d = p
	}
	return &d, nil
}

func decodePeriod(p rawPeriod, today dates.Date) (*dates.Range, error) {
	if p.Preset != nil && strings.TrimSpace(*p.Preset) != "" {
		preset := dates.Preset(strings.ToLower(strings.TrimSpace(*p.Preset)))
		if preset == "all" {
			r := dates.Range{From: expense.MinSpentAt, To: today.AddDays(1)}
			return &r, nil
		}
		r, err := preset.Resolve(today)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		return &r, nil
	}
	if p.From == nil && p.To == nil {
		return nil, nil
	}
	from, err := decodeDate("period.from", p.From, today)
	if err != nil {
		return nil, err
	}
	to, err := decodeDate("period.to", p.To, today)
	if err != nil {
		return nil, err
	}
	r := dates.Range{From: expense.MinSpentAt, To: today.AddDays(1)}
	if from != nil {
		r.From = *from
	}
	if to != nil {
		r.To = to.AddDays(1) // model gives an inclusive end date
	}
	if !r.Valid() {
		return nil, &FieldError{Field: "period", Err: errors.New("the end of the period is before its start")}
	}
	return &r, nil
}

func clip(s *string, max int) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	if utf8.RuneCountInString(v) > max {
		v = string([]rune(v)[:max])
	}
	return &v
}
