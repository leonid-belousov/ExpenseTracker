package expense

import (
	"context"
	"time"

	"expensetracker/internal/dates"
)

// Member is a configured family member (Telegram user).
type Member struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// MinSpentAt is the earliest accepted expense date.
var MinSpentAt = dates.New(2000, time.January, 1)

// List limits.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Service applies validation and ownership rules on top of a Store.
type Service struct {
	store   Store
	members []Member
	loc     *time.Location
	clock   dates.Clock
}

// NewService builds a service. members is the configured family; loc is the
// deployment time zone used to compute "today".
func NewService(store Store, members []Member, loc *time.Location, clock dates.Clock) *Service {
	if clock == nil {
		clock = dates.SystemClock{}
	}
	return &Service{store: store, members: members, loc: loc, clock: clock}
}

// Members returns the configured family members.
func (s *Service) Members() []Member { return append([]Member(nil), s.members...) }

// IsMember reports whether id is a configured family member.
func (s *Service) IsMember(id int64) bool {
	for _, m := range s.members {
		if m.ID == id {
			return true
		}
	}
	return false
}

// FamilyScope covers every configured member (dashboard scope).
func (s *Service) FamilyScope() Scope {
	ids := make([]int64, len(s.members))
	for i, m := range s.members {
		ids[i] = m.ID
	}
	return Scope{UserIDs: ids}
}

// Today returns the current date in the deployment time zone.
func (s *Service) Today() dates.Date { return dates.Of(s.clock.Now(), s.loc) }

// Location returns the deployment time zone.
func (s *Service) Location() *time.Location { return s.loc }

// InTx runs fn with a service bound to a single transaction.
func (s *Service) InTx(ctx context.Context, fn func(*Service) error) error {
	return s.store.InTx(ctx, func(tx Store) error {
		return fn(&Service{store: tx, members: s.members, loc: s.loc, clock: s.clock})
	})
}

// ClaimUpdate records a Telegram update id for idempotency.
func (s *Service) ClaimUpdate(ctx context.Context, updateID, userID int64) (bool, error) {
	return s.store.ClaimUpdate(ctx, updateID, userID)
}

// IsUpdateClaimed reports whether an update id was already processed.
func (s *Service) IsUpdateClaimed(ctx context.Context, updateID int64) (bool, error) {
	return s.store.IsUpdateClaimed(ctx, updateID)
}

// Ping checks storage connectivity.
func (s *Service) Ping(ctx context.Context) error { return s.store.Ping(ctx) }

// ValidateFields checks every editable field and normalises the comment.
func (s *Service) ValidateFields(scope Scope, f Fields) (Fields, error) {
	ve := &ValidationError{}
	if f.UserID <= 0 {
		ve.add("user_id", "user_id is required")
	} else if !s.IsMember(f.UserID) || !scope.Allows(f.UserID) {
		ve.add("user_id", "user_id must be a configured family member")
	}
	if err := f.Amount.ValidatePositive(); err != nil {
		ve.add("amount", err.Error())
	}
	if _, err := ParseCategory(string(f.Category)); err != nil {
		ve.add("category", err.Error())
	}
	if f.SpentAt.IsZero() {
		ve.add("spent_at", "spent_at is required")
	} else if f.SpentAt.Before(MinSpentAt) {
		ve.add("spent_at", "spent_at must not be before "+MinSpentAt.String())
	} else if f.SpentAt.After(s.Today()) {
		ve.add("spent_at", "spent_at must not be in the future")
	}
	f.Comment = NormalizeComment(f.Comment)
	validateComment(f.Comment, ve)
	return f, ve.orNil()
}

// Create validates and stores a new expense.
func (s *Service) Create(ctx context.Context, scope Scope, f Fields) (Expense, error) {
	f, err := s.ValidateFields(scope, f)
	if err != nil {
		return Expense{}, err
	}
	return s.store.Create(ctx, f)
}

// Get returns one expense inside the scope.
func (s *Service) Get(ctx context.Context, scope Scope, id int64) (Expense, error) {
	if id <= 0 {
		return Expense{}, ErrNotFound
	}
	return s.store.Get(ctx, scope, id)
}

// Update replaces the editable fields of an expense inside the scope.
func (s *Service) Update(ctx context.Context, scope Scope, id int64, f Fields) (Expense, error) {
	if id <= 0 {
		return Expense{}, ErrNotFound
	}
	f, err := s.ValidateFields(scope, f)
	if err != nil {
		return Expense{}, err
	}
	return s.store.Update(ctx, scope, id, f)
}

// Delete removes one expense inside the scope.
func (s *Service) Delete(ctx context.Context, scope Scope, id int64) error {
	if id <= 0 {
		return ErrNotFound
	}
	return s.store.Delete(ctx, scope, id)
}

// List returns a page of expenses matching the filter inside the scope.
func (s *Service) List(ctx context.Context, scope Scope, f Filter) (Page, error) {
	ve := &ValidationError{}
	if f.UserID != nil && (!s.IsMember(*f.UserID) || !scope.Allows(*f.UserID)) {
		ve.add("user_id", "user_id must be a configured family member")
	}
	if f.Period != nil && !f.Period.Valid() {
		ve.add("to", "the end of the period must be after its start")
	}
	if f.Category != nil {
		if _, err := ParseCategory(string(*f.Category)); err != nil {
			ve.add("category", err.Error())
		}
	}
	if f.Limit == 0 {
		f.Limit = DefaultLimit
	}
	if f.Limit < 1 || f.Limit > MaxLimit {
		ve.add("limit", "limit must be between 1 and 200")
	}
	if f.Offset < 0 {
		ve.add("offset", "offset must not be negative")
	}
	if err := ve.orNil(); err != nil {
		return Page{}, err
	}
	return s.store.List(ctx, scope, f)
}
