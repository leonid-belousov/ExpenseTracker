// Package expense contains the expense domain model, validation rules and the
// application service used by both the REST API and the Telegram bot.
package expense

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"expensetracker/internal/dates"
	"expensetracker/internal/money"
)

// Category is the canonical, storage-level category identifier. Localised
// labels live in the presentation layers only.
type Category string

const (
	Food          Category = "food"
	Transport     Category = "transport"
	Housing       Category = "housing"
	Entertainment Category = "entertainment"
	Health        Category = "health"
	Shopping      Category = "shopping"
	Other         Category = "other"
)

// Categories is the closed, ordered set of allowed categories.
var Categories = []Category{Food, Transport, Housing, Entertainment, Health, Shopping, Other}

// ParseCategory validates a canonical category string.
func ParseCategory(s string) (Category, error) {
	c := Category(strings.ToLower(strings.TrimSpace(s)))
	for _, v := range Categories {
		if c == v {
			return c, nil
		}
	}
	return "", fmt.Errorf("category must be one of %s", categoryList())
}

func categoryList() string {
	parts := make([]string, len(Categories))
	for i, c := range Categories {
		parts[i] = string(c)
	}
	return strings.Join(parts, ", ")
}

// MaxCommentLength is the maximum comment length in characters.
const MaxCommentLength = 500

// Expense is a stored expense.
type Expense struct {
	ID        int64        `json:"id"`
	UserID    int64        `json:"user_id"`
	Amount    money.Amount `json:"amount"`
	Category  Category     `json:"category"`
	SpentAt   dates.Date   `json:"spent_at"`
	Comment   *string      `json:"comment"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// Fields are the user-editable fields of an expense.
type Fields struct {
	UserID   int64
	Amount   money.Amount
	Category Category
	SpentAt  dates.Date
	Comment  *string
}

// Scope is the set of owners an operation may touch. Every store query is
// constrained to it; an empty scope matches nothing.
type Scope struct {
	UserIDs []int64
}

// OwnerScope restricts operations to a single Telegram user.
func OwnerScope(userID int64) Scope { return Scope{UserIDs: []int64{userID}} }

// Allows reports whether userID is inside the scope.
func (s Scope) Allows(userID int64) bool {
	for _, id := range s.UserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// Order selects a deterministic result ordering.
type Order int

const (
	// OrderSpentDesc: newest expense date first, then newest id.
	OrderSpentDesc Order = iota
	// OrderCreatedDesc: most recently recorded first, then newest id.
	OrderCreatedDesc
)

// Filter narrows a list query. All set conditions are ANDed.
type Filter struct {
	UserID       *int64
	Period       *dates.Range // half-open
	Category     *Category
	Amount       *money.Amount
	TextContains string // case-insensitive substring of the comment
	Order        Order
	Limit        int
	Offset       int
}

// Page is a list result with aggregates over the whole filtered set (not just
// the returned page).
type Page struct {
	Items []Expense
	Total int
	Sum   money.Amount
}

// Store is the persistence port. Every method that reads or mutates existing
// rows takes a Scope and must enforce it inside the query.
type Store interface {
	Create(ctx context.Context, f Fields) (Expense, error)
	Get(ctx context.Context, scope Scope, id int64) (Expense, error)
	Update(ctx context.Context, scope Scope, id int64, f Fields) (Expense, error)
	Delete(ctx context.Context, scope Scope, id int64) error
	List(ctx context.Context, scope Scope, f Filter) (Page, error)
	// InTx runs fn atomically with a Store bound to one transaction.
	InTx(ctx context.Context, fn func(Store) error) error
	// ClaimUpdate durably records a Telegram update id. It returns false if
	// the id was already claimed (i.e. the update is a retry).
	ClaimUpdate(ctx context.Context, updateID, userID int64) (bool, error)
	// IsUpdateClaimed checks without claiming.
	IsUpdateClaimed(ctx context.Context, updateID int64) (bool, error)
	// AppendChat stores bot conversation messages of one user and deletes
	// that user's messages older than pruneBefore.
	AppendChat(ctx context.Context, userID int64, msgs []ChatMessage, pruneBefore time.Time) error
	// RecentChat returns up to limit of the user's messages created at or
	// after since, oldest first.
	RecentChat(ctx context.Context, userID int64, since time.Time, limit int) ([]ChatMessage, error)
	Ping(ctx context.Context) error
}

// ChatRole is the author of a ChatMessage.
type ChatRole string

const (
	ChatUser      ChatRole = "user"
	ChatAssistant ChatRole = "assistant"
)

// MaxChatMessageLength bounds one stored conversation message.
const MaxChatMessageLength = 4000

// ChatMessage is one message of a member's conversation with the bot; it is
// the bot's short-term memory.
type ChatMessage struct {
	Role      ChatRole
	Content   string
	CreatedAt time.Time
}

// Errors returned by the service and stores.
var (
	ErrNotFound = errors.New("expense not found")
)

// ValidationError carries per-field messages.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for k, v := range e.Fields {
		parts = append(parts, k+": "+v)
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

func (e *ValidationError) add(field, msg string) {
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	if _, exists := e.Fields[field]; !exists {
		e.Fields[field] = msg
	}
}

func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

// NewValidationError builds a single-field validation error.
func NewValidationError(field, msg string) error {
	return &ValidationError{Fields: map[string]string{field: msg}}
}

// NormalizeComment trims the comment, strips control characters and returns
// nil for an empty value.
func NormalizeComment(c *string) *string {
	if c == nil {
		return nil
	}
	s := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, *c)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return nil
	}
	return &s
}

func validateComment(c *string, ve *ValidationError) {
	if c != nil && utf8.RuneCountInString(*c) > MaxCommentLength {
		ve.add("comment", fmt.Sprintf("comment must be at most %d characters", MaxCommentLength))
	}
}
