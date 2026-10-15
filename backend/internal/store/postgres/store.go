// Package postgres implements expense.Store on PostgreSQL using pgx. All
// queries are parameterised and every read/mutation of existing rows is
// constrained by the caller's owner scope inside SQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/money"
)

type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store is a PostgreSQL-backed expense.Store.
type Store struct {
	pool *pgxpool.Pool
	db   dbtx
}

// New wraps a pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool, db: pool} }

// Connect opens a pool and waits (with retries) until the database answers,
// so start-up does not depend on container timing.
func Connect(ctx context.Context, url string, wait time.Duration) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("DATABASE_URL is not a valid PostgreSQL connection string")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	deadline := time.Now().Add(wait)
	for {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pctx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			pool.Close()
			return nil, fmt.Errorf("database not reachable after %s: %w", wait, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

const selectCols = `id, user_id, amount::text, category, spent_at::text, comment, created_at, updated_at`

func scanExpense(row pgx.Row) (expense.Expense, error) {
	var (
		e                 expense.Expense
		amount, cat, date string
	)
	if err := row.Scan(&e.ID, &e.UserID, &amount, &cat, &date, &e.Comment, &e.CreatedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return expense.Expense{}, expense.ErrNotFound
		}
		return expense.Expense{}, err
	}
	a, err := money.Parse(amount)
	if err != nil {
		return expense.Expense{}, fmt.Errorf("decode amount: %w", err)
	}
	d, err := dates.Parse(date)
	if err != nil {
		return expense.Expense{}, fmt.Errorf("decode spent_at: %w", err)
	}
	e.Amount, e.Category, e.SpentAt = a, expense.Category(cat), d
	e.CreatedAt, e.UpdatedAt = e.CreatedAt.UTC(), e.UpdatedAt.UTC()
	return e, nil
}

func (s *Store) Ping(ctx context.Context) error {
	if s.pool == nil {
		return nil
	}
	return s.pool.Ping(ctx)
}

func (s *Store) Create(ctx context.Context, f expense.Fields) (expense.Expense, error) {
	row := s.db.QueryRow(ctx, `
		INSERT INTO expenses (user_id, amount, category, spent_at, comment)
		VALUES ($1, $2::numeric, $3, $4::date, $5)
		RETURNING `+selectCols,
		f.UserID, f.Amount.String(), string(f.Category), f.SpentAt.String(), f.Comment)
	return scanExpense(row)
}

func (s *Store) Get(ctx context.Context, scope expense.Scope, id int64) (expense.Expense, error) {
	row := s.db.QueryRow(ctx, `SELECT `+selectCols+` FROM expenses WHERE id = $1 AND user_id = ANY($2)`, id, scope.UserIDs)
	return scanExpense(row)
}

func (s *Store) Update(ctx context.Context, scope expense.Scope, id int64, f expense.Fields) (expense.Expense, error) {
	row := s.db.QueryRow(ctx, `
		UPDATE expenses
		SET user_id = $3, amount = $4::numeric, category = $5, spent_at = $6::date, comment = $7, updated_at = now()
		WHERE id = $1 AND user_id = ANY($2)
		RETURNING `+selectCols,
		id, scope.UserIDs, f.UserID, f.Amount.String(), string(f.Category), f.SpentAt.String(), f.Comment)
	return scanExpense(row)
}

func (s *Store) Delete(ctx context.Context, scope expense.Scope, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM expenses WHERE id = $1 AND user_id = ANY($2)`, id, scope.UserIDs)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return expense.ErrNotFound
	}
	return nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Store) List(ctx context.Context, scope expense.Scope, f expense.Filter) (expense.Page, error) {
	where := []string{"user_id = ANY($1)"}
	args := []any{scope.UserIDs}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.UserID != nil {
		add("user_id = $%d", *f.UserID)
	}
	if f.Period != nil {
		add("spent_at >= $%d::date", f.Period.From.String())
		add("spent_at < $%d::date", f.Period.To.String())
	}
	if f.Category != nil {
		add("category = $%d", string(*f.Category))
	}
	if f.Amount != nil {
		add("amount = $%d::numeric", f.Amount.String())
	}
	if f.TextContains != "" {
		add(`comment ILIKE '%%' || $%d || '%%' ESCAPE '\'`, escapeLike(f.TextContains))
	}
	cond := strings.Join(where, " AND ")

	var (
		page  expense.Page
		sum   string
		total int64
	)
	if err := s.db.QueryRow(ctx, `SELECT count(*), COALESCE(sum(amount), 0)::text FROM expenses WHERE `+cond, args...).Scan(&total, &sum); err != nil {
		return page, err
	}
	a, err := money.Parse(sum)
	if err != nil {
		return page, fmt.Errorf("decode sum: %w", err)
	}
	page.Total, page.Sum = int(total), a

	order := "spent_at DESC, id DESC"
	if f.Order == expense.OrderCreatedDesc {
		order = "created_at DESC, id DESC"
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.Query(ctx, fmt.Sprintf(`SELECT %s FROM expenses WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`,
		selectCols, cond, order, len(args)-1, len(args)), args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	page.Items = []expense.Expense{}
	for rows.Next() {
		e, err := scanExpense(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, e)
	}
	return page, rows.Err()
}

func (s *Store) InTx(ctx context.Context, fn func(expense.Store) error) error {
	if s.pool == nil {
		return errors.New("nested transactions are not supported")
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(&Store{db: tx})
	})
}

func (s *Store) ClaimUpdate(ctx context.Context, updateID, userID int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `INSERT INTO telegram_updates (update_id, user_id) VALUES ($1, $2) ON CONFLICT (update_id) DO NOTHING`, updateID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) AppendChat(ctx context.Context, userID int64, msgs []expense.ChatMessage, pruneBefore time.Time) error {
	for _, m := range msgs {
		if _, err := s.db.Exec(ctx, `INSERT INTO chat_messages (user_id, role, content) VALUES ($1, $2, $3)`,
			userID, string(m.Role), m.Content); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(ctx, `DELETE FROM chat_messages WHERE user_id = $1 AND created_at < $2`, userID, pruneBefore)
	return err
}

func (s *Store) RecentChat(ctx context.Context, userID int64, since time.Time, limit int) ([]expense.ChatMessage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT role, content, created_at FROM (
			SELECT id, role, content, created_at FROM chat_messages
			WHERE user_id = $1 AND created_at >= $2
			ORDER BY created_at DESC, id DESC LIMIT $3
		) recent ORDER BY created_at, id`, userID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []expense.ChatMessage
	for rows.Next() {
		var m expense.ChatMessage
		var role string
		if err := rows.Scan(&role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Role, m.CreatedAt = expense.ChatRole(role), m.CreatedAt.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) IsUpdateClaimed(ctx context.Context, updateID int64) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM telegram_updates WHERE update_id = $1)`, updateID).Scan(&exists)
	return exists, err
}
