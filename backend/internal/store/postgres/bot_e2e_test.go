package postgres

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"expensetracker/internal/agent"
	"expensetracker/internal/bot"
	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/telegram"
)

type recSender struct {
	mu   sync.Mutex
	last string
	n    int
}

func (r *recSender) SendMessage(_ context.Context, _ int64, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last, r.n = text, r.n+1
	return nil
}

// TestBotEndToEndOnPostgres drives the bot (with the rule-based model) against the
// real store: create, redelivery, isolation, ambiguity and delete.
func TestBotEndToEndOnPostgres(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	moscow, _ := time.LoadLocation("Europe/Moscow")
	clock := dates.FixedClock{T: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	svc := expense.NewService(s, []expense.Member{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}, moscow, clock)
	out := &recSender{}
	ag := agent.New(agent.RuleModel{}, agent.Config{Currency: "RUB", Language: "ru", StepTimeout: time.Second})
	b := bot.New(svc, ag, out, bot.Config{Currency: "RUB", Language: "ru"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	send := func(id, from int64, text string) string {
		before := out.n
		if err := b.HandleUpdate(ctx, telegram.Update{UpdateID: id, Message: &telegram.Message{
			From: &telegram.User{ID: from}, Chat: telegram.Chat{ID: from, Type: "private"}, Text: text}}); err != nil {
			t.Fatal(err)
		}
		if out.n == before {
			return ""
		}
		return out.last
	}

	if r := send(1, 1, "такси 1200 вчера"); !strings.Contains(r, "Записал") {
		t.Fatalf("create: %q", r)
	}
	if r := send(1, 1, "такси 1200 вчера"); r != "" {
		t.Fatalf("redelivery replied: %q", r)
	}
	send(2, 1, "Такси до работы 800")
	if r := send(3, 2, "удали такси"); !strings.Contains(r, "Не нашёл") {
		t.Fatalf("other user: %q", r)
	}
	if r := send(4, 1, "удали такси"); !strings.Contains(r, "подходящих трат (2)") {
		t.Fatalf("ambiguous: %q", r)
	}
	if r := send(5, 1, "удали такси 800"); !strings.Contains(r, "Удалил") {
		t.Fatalf("unique delete: %q", r)
	}
	if r := send(6, 1, "сколько потратил в этом месяце"); !strings.Contains(r, "1 200 RUB (1 шт.)") {
		t.Fatalf("summary: %q", r)
	}
	p, err := s.List(ctx, expense.Scope{UserIDs: []int64{1, 2}}, expense.Filter{Limit: 10})
	if err != nil || p.Total != 1 || p.Items[0].UserID != 1 || p.Items[0].SpentAt.String() != "2026-10-14" {
		t.Fatalf("final state %+v %v", p, err)
	}
}
