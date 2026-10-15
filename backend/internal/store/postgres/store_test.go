package postgres

// Integration tests against a real PostgreSQL. They run only when
// TEST_DATABASE_URL is set, e.g.:
//
//	TEST_DATABASE_URL=postgres://expenses:secret@127.0.0.1:55432/expenses_test?sslmode=disable go test ./internal/store/postgres/
//
// The tests drop and recreate their tables, so never point this at real data.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/money"
)

func setup(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := Connect(ctx, url, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS expenses, telegram_updates, chat_messages, schema_migrations`); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Migrate(ctx, pool, log); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool, log); err != nil { // idempotent
		t.Fatalf("second migrate: %v", err)
	}
	return New(pool), pool
}

func amt(t *testing.T, s string) money.Amount {
	a, err := money.ParsePositive(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCRUDAndScope(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	c := "кофе"
	e, err := s.Create(ctx, expense.Fields{UserID: 1, Amount: amt(t, "350.50"), Category: expense.Food, SpentAt: dates.New(2026, 10, 14), Comment: &c})
	if err != nil {
		t.Fatal(err)
	}
	if e.Amount.String() != "350.50" || e.SpentAt.String() != "2026-10-14" || e.CreatedAt.Location() != time.UTC {
		t.Fatalf("created %+v", e)
	}
	other := expense.OwnerScope(2)
	if _, err := s.Get(ctx, other, e.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("foreign get: %v", err)
	}
	if _, err := s.Update(ctx, other, e.ID, expense.Fields{UserID: 2, Amount: 1, Category: expense.Food, SpentAt: e.SpentAt}); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("foreign update: %v", err)
	}
	if err := s.Delete(ctx, other, e.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	if err := s.Delete(ctx, expense.Scope{}, e.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("empty scope delete: %v", err)
	}
	own := expense.OwnerScope(1)
	u, err := s.Update(ctx, own, e.ID, expense.Fields{UserID: 1, Amount: amt(t, "450"), Category: expense.Food, SpentAt: e.SpentAt})
	if err != nil || u.Amount.String() != "450.00" || u.Comment != nil || !u.UpdatedAt.After(e.UpdatedAt.Add(-time.Millisecond)) {
		t.Fatalf("update %+v %v", u, err)
	}
	if err := s.Delete(ctx, own, e.ID); err != nil {
		t.Fatal(err)
	}
}

func TestListFiltersSumsAndBoundaries(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	mk := func(user int64, a string, cat expense.Category, d dates.Date, comment string) {
		var cp *string
		if comment != "" {
			cp = &comment
		}
		if _, err := s.Create(ctx, expense.Fields{UserID: user, Amount: amt(t, a), Category: cat, SpentAt: d, Comment: cp}); err != nil {
			t.Fatal(err)
		}
	}
	mk(1, "0.10", expense.Food, dates.New(2026, 10, 11), "Кофе")         // Sunday
	mk(1, "0.20", expense.Food, dates.New(2026, 10, 12), "кофе с собой") // Monday
	mk(1, "1200", expense.Transport, dates.New(2026, 10, 18), "такси")   // Sunday end of week
	mk(1, "5", expense.Food, dates.New(2026, 10, 19), "100%_pure")       // next Monday
	mk(2, "999", expense.Food, dates.New(2026, 10, 12), "кофе")          // other user

	week := dates.Range{From: dates.New(2026, 10, 12), To: dates.New(2026, 10, 19)}
	p, err := s.List(ctx, expense.OwnerScope(1), expense.Filter{Period: &week, Limit: 50})
	if err != nil || p.Total != 2 || p.Sum.String() != "1200.20" {
		t.Fatalf("week %+v %v", p, err)
	}
	if p.Items[0].SpentAt.String() != "2026-10-18" {
		t.Fatal("ordering: spent_at desc")
	}
	p, _ = s.List(ctx, expense.OwnerScope(1), expense.Filter{TextContains: "КОФЕ", Limit: 50})
	if p.Total != 2 || p.Sum.String() != "0.30" {
		t.Fatalf("case-insensitive cyrillic text: %+v", p)
	}
	p, _ = s.List(ctx, expense.OwnerScope(1), expense.Filter{TextContains: "%_", Limit: 50})
	if p.Total != 1 {
		t.Fatalf("LIKE wildcards must be escaped: %+v", p)
	}
	fam := expense.Scope{UserIDs: []int64{1, 2}}
	food := expense.Food
	p, _ = s.List(ctx, fam, expense.Filter{Category: &food, Limit: 2, Offset: 0})
	if p.Total != 4 || len(p.Items) != 2 || p.Sum.String() != "1004.30" {
		t.Fatalf("family food: %+v", p)
	}
	a := amt(t, "999")
	user2 := int64(2)
	p, _ = s.List(ctx, fam, expense.Filter{Amount: &a, UserID: &user2, Limit: 10})
	if p.Total != 1 {
		t.Fatalf("amount filter: %+v", p)
	}
	p, _ = s.List(ctx, expense.OwnerScope(1), expense.Filter{Order: expense.OrderCreatedDesc, Limit: 1})
	if p.Items[0].Amount.String() != "5.00" {
		t.Fatalf("created order: %+v", p.Items[0])
	}
}

func TestClaimUpdateIsIdempotentAndTransactional(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	// Rolled-back claim leaves no trace.
	err := s.InTx(ctx, func(tx expense.Store) error {
		ok, err := tx.ClaimUpdate(ctx, 10, 1)
		if err != nil || !ok {
			t.Fatalf("claim %v %v", ok, err)
		}
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if done, _ := s.IsUpdateClaimed(ctx, 10); done {
		t.Fatal("rolled back claim persisted")
	}
	// Concurrent claims: exactly one wins.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.InTx(ctx, func(tx expense.Store) error {
				ok, err := tx.ClaimUpdate(ctx, 11, 1)
				if err != nil {
					return err
				}
				if ok {
					c := "x"
					if _, err := tx.Create(ctx, expense.Fields{UserID: 1, Amount: 100, Category: expense.Other, SpentAt: dates.New(2026, 1, 1), Comment: &c}); err != nil {
						return err
					}
					mu.Lock()
					wins++
					mu.Unlock()
				}
				return nil
			})
		}()
	}
	wg.Wait()
	p, _ := s.List(ctx, expense.OwnerScope(1), expense.Filter{Limit: 50})
	if wins != 1 || p.Total != 1 {
		t.Fatalf("wins=%d rows=%d", wins, p.Total)
	}
}

func TestDatabaseConstraintsBackstopValidation(t *testing.T) {
	_, pool := setup(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO expenses (user_id, amount, category, spent_at) VALUES (1, 0, 'food', '2026-01-01')`,
		`INSERT INTO expenses (user_id, amount, category, spent_at) VALUES (1, 1, 'еда', '2026-01-01')`,
		`INSERT INTO expenses (user_id, amount, category, spent_at, comment) VALUES (1, 1, 'food', '2026-01-01', '')`,
	} {
		if _, err := pool.Exec(ctx, q); err == nil {
			t.Errorf("constraint not enforced: %s", q)
		}
	}
}

func TestChatMemory(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	msgs := []expense.ChatMessage{{Role: expense.ChatUser, Content: "удали такси"}, {Role: expense.ChatAssistant, Content: "Какую?\n[ids: 1, 2]"}}
	if err := s.AppendChat(ctx, 1, msgs, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendChat(ctx, 2, []expense.ChatMessage{{Role: expense.ChatUser, Content: "чужое"}}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecentChat(ctx, 1, time.Now().Add(-time.Minute), 10)
	if err != nil || len(got) != 2 || got[0].Role != expense.ChatUser || got[1].Content != msgs[1].Content {
		t.Fatalf("recent %+v %v", got, err)
	}
	if got, _ := s.RecentChat(ctx, 1, time.Now().Add(-time.Minute), 1); len(got) != 1 || got[0].Role != expense.ChatAssistant {
		t.Fatalf("limit keeps the newest: %+v", got)
	}
	// Pruning removes the user's old messages only.
	if err := s.AppendChat(ctx, 1, nil, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.RecentChat(ctx, 1, time.Time{}, 10); len(got) != 0 {
		t.Fatalf("not pruned: %+v", got)
	}
	if got, _ := s.RecentChat(ctx, 2, time.Time{}, 10); len(got) != 1 {
		t.Fatalf("pruned another user: %+v", got)
	}
}
