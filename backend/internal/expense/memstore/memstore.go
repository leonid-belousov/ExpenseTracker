// Package memstore is an in-memory expense.Store used by unit tests. It
// mirrors the PostgreSQL store's scoping, filtering and ordering semantics.
package memstore

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"expensetracker/internal/expense"
	"expensetracker/internal/money"
)

type state struct {
	nextID   int64
	expenses map[int64]expense.Expense
	updates  map[int64]int64
}

func (s *state) clone() *state {
	c := &state{nextID: s.nextID, expenses: make(map[int64]expense.Expense, len(s.expenses)), updates: make(map[int64]int64, len(s.updates))}
	for k, v := range s.expenses {
		c.expenses[k] = v
	}
	for k, v := range s.updates {
		c.updates[k] = v
	}
	return c
}

// Store is a concurrency-safe in-memory store.
type Store struct {
	mu   *sync.Mutex
	st   *state
	inTx bool
	Now  func() time.Time
	// FailNext, when set, makes the next mutating call return this error.
	FailNext error
}

// New returns an empty store.
func New() *Store {
	return &Store{mu: &sync.Mutex{}, st: &state{nextID: 1, expenses: map[int64]expense.Expense{}, updates: map[int64]int64{}}, Now: time.Now}
}

func (s *Store) lock() func() {
	if s.inTx {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

func (s *Store) takeFailure() error {
	err := s.FailNext
	s.FailNext = nil
	return err
}

func (s *Store) Ping(context.Context) error { return nil }

func (s *Store) Create(_ context.Context, f expense.Fields) (expense.Expense, error) {
	defer s.lock()()
	if err := s.takeFailure(); err != nil {
		return expense.Expense{}, err
	}
	now := s.Now().UTC()
	e := expense.Expense{ID: s.st.nextID, UserID: f.UserID, Amount: f.Amount, Category: f.Category, SpentAt: f.SpentAt, Comment: copyStr(f.Comment), CreatedAt: now, UpdatedAt: now}
	s.st.nextID++
	s.st.expenses[e.ID] = e
	return e, nil
}

func (s *Store) Get(_ context.Context, scope expense.Scope, id int64) (expense.Expense, error) {
	defer s.lock()()
	e, ok := s.st.expenses[id]
	if !ok || !scope.Allows(e.UserID) {
		return expense.Expense{}, expense.ErrNotFound
	}
	return e, nil
}

func (s *Store) Update(_ context.Context, scope expense.Scope, id int64, f expense.Fields) (expense.Expense, error) {
	defer s.lock()()
	if err := s.takeFailure(); err != nil {
		return expense.Expense{}, err
	}
	e, ok := s.st.expenses[id]
	if !ok || !scope.Allows(e.UserID) {
		return expense.Expense{}, expense.ErrNotFound
	}
	e.UserID, e.Amount, e.Category, e.SpentAt, e.Comment = f.UserID, f.Amount, f.Category, f.SpentAt, copyStr(f.Comment)
	e.UpdatedAt = s.Now().UTC()
	s.st.expenses[id] = e
	return e, nil
}

func (s *Store) Delete(_ context.Context, scope expense.Scope, id int64) error {
	defer s.lock()()
	if err := s.takeFailure(); err != nil {
		return err
	}
	e, ok := s.st.expenses[id]
	if !ok || !scope.Allows(e.UserID) {
		return expense.ErrNotFound
	}
	delete(s.st.expenses, id)
	return nil
}

func (s *Store) List(_ context.Context, scope expense.Scope, f expense.Filter) (expense.Page, error) {
	defer s.lock()()
	var items []expense.Expense
	var sum money.Amount
	for _, e := range s.st.expenses {
		if !scope.Allows(e.UserID) {
			continue
		}
		if f.UserID != nil && e.UserID != *f.UserID {
			continue
		}
		if f.Period != nil && !f.Period.Contains(e.SpentAt) {
			continue
		}
		if f.Category != nil && e.Category != *f.Category {
			continue
		}
		if f.Amount != nil && e.Amount != *f.Amount {
			continue
		}
		if f.TextContains != "" && (e.Comment == nil || !strings.Contains(strings.ToLower(*e.Comment), strings.ToLower(f.TextContains))) {
			continue
		}
		items = append(items, e)
		sum += e.Amount
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if f.Order == expense.OrderCreatedDesc {
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.After(b.CreatedAt)
			}
		} else if !a.SpentAt.Equal(b.SpentAt) {
			return a.SpentAt.After(b.SpentAt)
		}
		return a.ID > b.ID
	})
	total := len(items)
	if f.Offset >= len(items) {
		items = nil
	} else {
		items = items[f.Offset:]
	}
	if f.Limit > 0 && len(items) > f.Limit {
		items = items[:f.Limit]
	}
	return expense.Page{Items: items, Total: total, Sum: sum}, nil
}

func (s *Store) InTx(ctx context.Context, fn func(expense.Store) error) error {
	if s.inTx {
		return errors.New("nested transactions are not supported")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := &Store{mu: s.mu, st: s.st.clone(), inTx: true, Now: s.Now, FailNext: s.FailNext}
	if err := fn(tx); err != nil {
		s.FailNext = tx.FailNext
		return err
	}
	s.st = tx.st
	s.FailNext = tx.FailNext
	return nil
}

func (s *Store) ClaimUpdate(_ context.Context, updateID, userID int64) (bool, error) {
	defer s.lock()()
	if _, ok := s.st.updates[updateID]; ok {
		return false, nil
	}
	s.st.updates[updateID] = userID
	return true, nil
}

func (s *Store) IsUpdateClaimed(_ context.Context, updateID int64) (bool, error) {
	defer s.lock()()
	_, ok := s.st.updates[updateID]
	return ok, nil
}

// All returns every stored expense ordered by id (test helper).
func (s *Store) All() []expense.Expense {
	defer s.lock()()
	out := make([]expense.Expense, 0, len(s.st.expenses))
	for _, e := range s.st.expenses {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func copyStr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
