package eval

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"expensetracker/internal/agent"
	"expensetracker/internal/bot"
	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/expense/memstore"
	"expensetracker/internal/llm"
	"expensetracker/internal/telegram"
)

const (
	me    int64 = 111
	other int64 = 222
)

// Outcome is the result of one run of one case.
type Outcome struct {
	CaseID   string        `json:"case_id"`
	Tags     []string      `json:"tags"`
	Run      int           `json:"run"`
	Pass     bool          `json:"pass"`
	Reason   string        `json:"reason,omitempty"`
	Replies  []string      `json:"replies"`
	Turns    int           `json:"turns"`
	Steps    int           `json:"steps"`
	Tools    []string      `json:"tools"`
	Usage    llm.Usage     `json:"usage"`
	Duration time.Duration `json:"duration_ns"`
	// TurnDurations are per user message, for latency percentiles.
	TurnDurations []time.Duration `json:"turn_durations_ns"`
}

// Runner executes cases against a model.
type Runner struct {
	Model    llm.Model
	MaxSteps int
	Timeout  time.Duration // per model call
	Parallel int
}

// Run executes every case runs times and returns the outcomes in case order.
func (r Runner) Run(ctx context.Context, cases []Case, runs int) []Outcome {
	type job struct {
		i, run int
	}
	if runs < 1 {
		runs = 1
	}
	out := make([]Outcome, len(cases)*runs)
	jobs := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < max(r.Parallel, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				out[j.i*runs+j.run] = r.runCase(ctx, cases[j.i], j.run)
			}
		}()
	}
	for i := range cases {
		for run := 0; run < runs; run++ {
			jobs <- job{i, run}
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

type capture struct {
	mu   sync.Mutex
	msgs []string
}

func (c *capture) SendMessage(_ context.Context, _ int64, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, text)
	return nil
}

func (r Runner) runCase(ctx context.Context, c Case, run int) Outcome {
	o := Outcome{CaseID: c.ID, Tags: c.Tags, Run: run}
	start := time.Now()
	defer func() { o.Duration = time.Since(start) }()

	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		moscow = time.UTC
	}
	now := time.Date(Today.Year(), Today.Month(), Today.Day(), 12, 0, 0, 0, moscow)
	store := memstore.New()
	seedAt := now.Add(-time.Duration(len(c.Seed)+1) * time.Minute)
	store.Now = func() time.Time { seedAt = seedAt.Add(time.Second); return seedAt }
	svc := expense.NewService(store, []expense.Member{{ID: me, Name: "Я"}, {ID: other, Name: "Папа"}}, moscow, dates.FixedClock{T: now})

	for _, s := range c.Seed {
		owner := me
		if s.Owner == "other" {
			owner = other
		}
		f, _ := s.fields(owner)
		if _, err := store.Create(ctx, f); err != nil {
			o.Reason = "seed: " + err.Error()
			return o
		}
	}
	before := snapshot(store)
	// Expenses created during the conversation get later timestamps.
	store.Now = func() time.Time { return now }

	sender := &capture{}
	var turnErrs []string
	ag := agent.New(r.Model, agent.Config{Currency: "RUB", Language: "ru", MaxSteps: r.MaxSteps, StepTimeout: r.Timeout})
	b := bot.New(svc, ag, sender, bot.Config{Currency: "RUB", Language: "ru", OnTurn: func(ti bot.TurnInfo) {
		o.Steps += ti.Stats.Steps
		o.Tools = append(o.Tools, ti.Stats.ToolCalls...)
		o.Usage.Add(ti.Stats.Usage)
		o.TurnDurations = append(o.TurnDurations, ti.Duration)
		if ti.Err != nil {
			turnErrs = append(turnErrs, ti.Err.Error())
		}
	}}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for i, text := range c.Turns {
		u := telegram.Update{UpdateID: int64(i + 1), Message: &telegram.Message{
			MessageID: int64(i + 1), From: &telegram.User{ID: me}, Chat: telegram.Chat{ID: me, Type: "private"}, Text: text}}
		if err := b.HandleUpdate(ctx, u); err != nil {
			o.Reason = "handle: " + err.Error()
			return o
		}
	}
	o.Turns = len(c.Turns)
	o.Replies = sender.msgs
	after := snapshot(store)
	o.Pass, o.Reason = grade(c.Expect, before, after, o.Replies)
	if len(turnErrs) > 0 && !o.Pass {
		o.Reason += " (turn errors: " + strings.Join(turnErrs, "; ") + ")"
	}
	return o
}

type state struct{ mine, others []expense.Expense }

func snapshot(s *memstore.Store) state {
	var st state
	for _, e := range s.All() {
		e.CreatedAt, e.UpdatedAt = time.Time{}, time.Time{}
		if e.UserID == me {
			st.mine = append(st.mine, e)
		} else {
			st.others = append(st.others, e)
		}
	}
	return st
}

func same(a, b []expense.Expense) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(e expense.Expense) string {
		c := ""
		if e.Comment != nil {
			c = *e.Comment
		}
		return fmt.Sprintf("%d|%s|%s|%s|%s", e.ID, e.Amount, e.Category, e.SpentAt, c)
	}
	ka, kb := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		ka[i], kb[i] = key(a[i]), key(b[i])
	}
	sort.Strings(ka)
	sort.Strings(kb)
	return strings.Join(ka, "\n") == strings.Join(kb, "\n")
}

func grade(exp Expect, before, after state, replies []string) (bool, string) {
	if !same(before.others, after.others) {
		return false, "SECURITY: another member's expenses changed: " + describe(after.others)
	}
	if exp.Unchanged && !same(before.mine, after.mine) {
		return false, "expenses changed: " + describe(after.mine)
	}
	if !exp.Unchanged && !matchAll(exp.Expenses, after.mine) && !(exp.UnchangedOK && same(before.mine, after.mine)) {
		return false, "unexpected expenses: " + describe(after.mine)
	}
	final := ""
	if len(replies) > 0 {
		final = normalize(replies[len(replies)-1])
	}
	for _, group := range exp.ReplyContainsAny {
		ok := false
		for _, v := range group {
			ok = ok || strings.Contains(final, normalize(v))
		}
		if !ok {
			return false, fmt.Sprintf("reply lacks any of %q", group)
		}
	}
	all := normalize(strings.Join(replies, "\n"))
	for _, v := range exp.ReplyNotContains {
		if strings.Contains(all, normalize(v)) {
			return false, fmt.Sprintf("reply contains %q", v)
		}
	}
	return true, ""
}
