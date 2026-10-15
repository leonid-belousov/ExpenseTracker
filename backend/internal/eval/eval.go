// Package eval measures the bot agent's quality on a fixed dataset of
// conversations. Each case seeds expenses, sends one or more messages through
// the real bot pipeline (agent, tools, validation, transaction, memory) with
// an in-memory store, and grades the outcome deterministically:
//
//   - the sender's final expenses must match the expected set exactly
//     (or stay unchanged);
//   - the other family member's expenses must never change (checked in every
//     case);
//   - optional checks on the final reply text (numbers, wording).
//
// No LLM judge is used: every check is code, so results are reproducible and
// cheap to compute.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/money"
)

// Today is the fixed "today" of every case: Thursday 2026-10-15.
var Today = dates.New(2026, 10, 15)

// Seed is an expense that exists before the conversation.
type Seed struct {
	Owner    string `json:"owner"` // "me" (the sender) or "other"
	Amount   string `json:"amount"`
	Category string `json:"category"`
	Date     string `json:"date"`
	Comment  string `json:"comment"`
}

// ExpectedExpense describes one of the sender's expenses after the
// conversation. Empty fields are not checked.
type ExpectedExpense struct {
	Amount             string   `json:"amount"`
	Category           string   `json:"category,omitempty"`
	CategoryAny        []string `json:"category_any,omitempty"`
	Date               string   `json:"date,omitempty"`
	CommentContains    string   `json:"comment_contains,omitempty"`
	CommentNotContains string   `json:"comment_not_contains,omitempty"`
}

// Expect is a case's success criteria.
type Expect struct {
	// Expenses is the complete expected set of the sender's expenses.
	Expenses []ExpectedExpense `json:"expenses,omitempty"`
	// Unchanged requires the sender's expenses to equal the seed exactly.
	Unchanged bool `json:"unchanged,omitempty"`
	// ReplyContainsAny: for each group, the final reply must contain at
	// least one of the variants (case-insensitive; digit groups are joined,
	// so "1 450" matches "1450").
	ReplyContainsAny [][]string `json:"reply_contains_any,omitempty"`
	// ReplyNotContains: none of these may appear in any reply.
	ReplyNotContains []string `json:"reply_not_contains,omitempty"`
	// UnchangedOK also accepts unchanged expenses when Expenses is set (for
	// requests a model may either fulfil safely or decline).
	UnchangedOK bool `json:"unchanged_ok,omitempty"`
}

// Case is one eval scenario.
type Case struct {
	ID          string   `json:"id"`
	Tags        []string `json:"tags"`
	Description string   `json:"description,omitempty"`
	Seed        []Seed   `json:"seed,omitempty"`
	Turns       []string `json:"turns"`
	Expect      Expect   `json:"expect"`
}

// Load reads and validates a dataset file.
func Load(path string) ([]Case, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Cases []Case `json:"cases"`
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, c := range file.Cases {
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("case %q: %w", c.ID, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
	}
	return file.Cases, nil
}

func (c Case) validate() error {
	if c.ID == "" || len(c.Turns) == 0 || len(c.Tags) == 0 {
		return fmt.Errorf("id, tags and turns are required")
	}
	if c.Expect.Unchanged == (c.Expect.Expenses != nil) {
		return fmt.Errorf("set exactly one of expect.unchanged and expect.expenses")
	}
	for _, s := range c.Seed {
		if s.Owner != "me" && s.Owner != "other" {
			return fmt.Errorf("seed owner must be me or other")
		}
		if _, err := s.fields(1); err != nil {
			return err
		}
	}
	for _, e := range c.Expect.Expenses {
		if _, err := money.ParsePositive(e.Amount); err != nil {
			return fmt.Errorf("expected amount %q: %w", e.Amount, err)
		}
		if e.Date != "" {
			if _, err := dates.Parse(e.Date); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s Seed) fields(owner int64) (expense.Fields, error) {
	a, err := money.ParsePositive(s.Amount)
	if err != nil {
		return expense.Fields{}, fmt.Errorf("seed amount %q: %w", s.Amount, err)
	}
	c, err := expense.ParseCategory(s.Category)
	if err != nil {
		return expense.Fields{}, err
	}
	d, err := dates.Parse(s.Date)
	if err != nil {
		return expense.Fields{}, err
	}
	f := expense.Fields{UserID: owner, Amount: a, Category: c, SpentAt: d}
	if s.Comment != "" {
		cm := s.Comment
		f.Comment = &cm
	}
	return f, nil
}

func (e ExpectedExpense) matches(x expense.Expense) bool {
	if a, _ := money.ParsePositive(e.Amount); a != x.Amount {
		return false
	}
	if e.Category != "" && string(x.Category) != e.Category {
		return false
	}
	if len(e.CategoryAny) > 0 {
		ok := false
		for _, c := range e.CategoryAny {
			ok = ok || string(x.Category) == c
		}
		if !ok {
			return false
		}
	}
	if e.Date != "" && x.SpentAt.String() != e.Date {
		return false
	}
	comment := ""
	if x.Comment != nil {
		comment = strings.ToLower(*x.Comment)
	}
	if e.CommentContains != "" && !strings.Contains(comment, strings.ToLower(e.CommentContains)) {
		return false
	}
	if e.CommentNotContains != "" && strings.Contains(comment, strings.ToLower(e.CommentNotContains)) {
		return false
	}
	return true
}

// matchAll reports whether actual can be paired one-to-one with expected.
func matchAll(expected []ExpectedExpense, actual []expense.Expense) bool {
	if len(expected) != len(actual) {
		return false
	}
	used := make([]bool, len(actual))
	var try func(i int) bool
	try = func(i int) bool {
		if i == len(expected) {
			return true
		}
		for j, x := range actual {
			if !used[j] && expected[i].matches(x) {
				used[j] = true
				if try(i + 1) {
					return true
				}
				used[j] = false
			}
		}
		return false
	}
	return try(0)
}

var digitGroup = regexp.MustCompile(`(\d)[\s\x{00a0}\x{202f}](\d{3})`)

// normalize lowercases text and joins digit groups ("1 450" -> "1450").
func normalize(s string) string {
	s = strings.ToLower(s)
	for {
		n := digitGroup.ReplaceAllString(s, "$1$2")
		if n == s {
			return s
		}
		s = n
	}
}

func describe(es []expense.Expense) string {
	parts := make([]string, len(es))
	for i, e := range es {
		c := ""
		if e.Comment != nil {
			c = *e.Comment
		}
		parts[i] = fmt.Sprintf("%s %s %s %q", e.SpentAt, e.Amount, e.Category, c)
	}
	return "[" + strings.Join(parts, "; ") + "]"
}
