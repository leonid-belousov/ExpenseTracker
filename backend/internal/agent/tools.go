package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/llm"
	"expensetracker/internal/money"
)

// Tool names.
const (
	ToolAddExpenses       = "add_expenses"
	ToolFindExpenses      = "find_expenses"
	ToolSummarizeExpenses = "summarize_expenses"
	ToolUpdateExpense     = "update_expense"
	ToolDeleteExpense     = "delete_expense"
)

// Limits on what one agent turn may do.
const (
	maxAddItems     = 10
	maxFindLimit    = 20
	maxMutations    = 20
	defaultFindRows = 10
)

// ---- JSON schema helpers (strict: every property required, nullable when optional) ----

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func nullable(s map[string]any) map[string]any {
	return map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
}

func enum(desc string, values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

func object(props map[string]any) map[string]any {
	req := make([]string, 0, len(props))
	for k := range props {
		req = append(req, k)
	}
	sort.Strings(req) // deterministic bytes keep the prompt cache stable
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

func categoryNames() []string {
	out := make([]string, len(expense.Categories))
	for i, c := range expense.Categories {
		out[i] = string(c)
	}
	return out
}

func presetNames() []string {
	out := []string{"all_time"}
	for _, p := range dates.Presets {
		out = append(out, string(p))
	}
	return out
}

const dateDesc = `"today", "yesterday", "day_before_yesterday" or "YYYY-MM-DD"`

func periodProps(defaultDesc string) map[string]any {
	return map[string]any{
		"preset": nullable(enum("Relative period; weeks start on Monday. "+defaultDesc, presetNames())),
		"from":   nullable(str("Inclusive start date, " + dateDesc + ". Use instead of preset for explicit dates.")),
		"to":     nullable(str("Inclusive end date, " + dateDesc + ".")),
	}
}

func merge(ms ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// Tools returns the tool definitions. They never take an owner: the
// application always applies them to the sender's own expenses.
func Tools() []llm.ToolSpec {
	cat := enum("Expense category.", categoryNames())
	return []llm.ToolSpec{
		{
			Name: ToolAddExpenses,
			Description: "Record one or more new expenses of the user (at most 10 per call). " +
				"All items are validated first; if any item is invalid nothing is saved.",
			Schema: object(map[string]any{
				"items": map[string]any{"type": "array", "items": object(map[string]any{
					"amount":   str(`Amount as written by the user, decimal, e.g. "350" or "99.90". Must be > 0 with at most 2 decimals.`),
					"category": cat,
					"date":     nullable(str("Date of the expense, " + dateDesc + "; null means today. Future dates are rejected.")),
					"comment":  nullable(str(`Short description in the user's words, e.g. "кофе", "такси". No amount or date in it.`)),
				})},
			}),
		},
		{
			Name: ToolFindExpenses,
			Description: "Find the user's expenses. All given filters are combined with AND. " +
				"Returns matching expenses with ids, plus the total count and exact sum of all matches. " +
				"Use it to list expenses and to locate an expense before changing or deleting it.",
			Schema: object(merge(periodProps("If preset, from and to are all null, all time is searched."), map[string]any{
				"category": nullable(cat),
				"amount":   nullable(str(`Exact amount, e.g. "350".`)),
				"text":     nullable(str(`Case-insensitive substring of the comment. Prefer a word stem ("такс" matches "такси", "Такси до работы").`)),
				"sort":     enum(`"recently_added": newest recorded first ("the last one" = recently_added with limit 1); "by_date": newest expense date first.`, []string{"recently_added", "by_date"}),
				"limit":    nullable(map[string]any{"type": "integer", "description": "Maximum expenses to return, 1-20; null means 10."}),
			})),
		},
		{
			Name:        ToolSummarizeExpenses,
			Description: "Total amount and count of the user's expenses for a period, optionally for one category, with a per-category breakdown.",
			Schema: object(merge(periodProps("If preset, from and to are all null, the current month is used."), map[string]any{
				"category": nullable(cat),
			})),
		},
		{
			Name:        ToolUpdateExpense,
			Description: "Change fields of one of the user's expenses. Only pass the new values; null keeps a field unchanged. The id must come from a tool result.",
			Schema: object(map[string]any{
				"id":       map[string]any{"type": "integer", "description": "Expense id from a tool result."},
				"amount":   nullable(str(`New amount, e.g. "450".`)),
				"category": nullable(cat),
				"date":     nullable(str("New date, " + dateDesc + ".")),
				"comment":  nullable(str("New comment.")),
			}),
		},
		{
			Name:        ToolDeleteExpense,
			Description: "Delete one of the user's expenses. The id must come from a tool result.",
			Schema: object(map[string]any{
				"id": map[string]any{"type": "integer", "description": "Expense id from a tool result."},
			}),
		},
	}
}

// ---- execution ----

// ChangeKind classifies a mutation.
type ChangeKind string

const (
	Created ChangeKind = "created"
	Updated ChangeKind = "updated"
	Deleted ChangeKind = "deleted"
)

// Change is one mutation performed during a turn. Before is set for updates
// and deletes, After for creates and updates.
type Change struct {
	Kind   ChangeKind
	Before expense.Expense
	After  expense.Expense
}

// toolError is reported back to the model as an is_error tool result.
type toolError struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"error"`
}

func (e *toolError) Error() string { return e.Message }

func badInput(field, format string, a ...any) error {
	return &toolError{Field: field, Message: fmt.Sprintf(format, a...)}
}

// toolbox executes tool calls for one actor inside one transaction.
type toolbox struct {
	svc      *expense.Service
	actor    int64
	currency string
	today    dates.Date
	seen     map[int64]bool // ids the model has legitimately learned
	changes  []Change
	refs     []int64
}

func (tb *toolbox) scope() expense.Scope { return expense.OwnerScope(tb.actor) }

func (tb *toolbox) remember(id int64) {
	if !tb.seen[id] {
		tb.seen[id] = true
	}
	for _, r := range tb.refs {
		if r == id {
			return
		}
	}
	tb.refs = append(tb.refs, id)
}

// run executes one call. A *toolError means the model should see the error
// and may retry; any other error aborts the turn (and rolls it back).
func (tb *toolbox) run(ctx context.Context, call llm.ToolCall) (any, error) {
	switch call.Name {
	case ToolAddExpenses:
		return tb.add(ctx, call.Input)
	case ToolFindExpenses:
		return tb.find(ctx, call.Input)
	case ToolSummarizeExpenses:
		return tb.summarize(ctx, call.Input)
	case ToolUpdateExpense:
		return tb.update(ctx, call.Input)
	case ToolDeleteExpense:
		return tb.delete(ctx, call.Input)
	}
	return nil, badInput("", "unknown tool %q", call.Name)
}

// decode is lenient about unknown fields (e.g. an injected "user_id"): they
// are ignored, never used.
func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return badInput("", "arguments are not valid JSON for this tool: %v", err)
	}
	return nil
}

type expenseView struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Amount   string `json:"amount"`
	Category string `json:"category"`
	Comment  string `json:"comment,omitempty"`
	AddedAt  string `json:"added_at,omitempty"`
}

func view(e expense.Expense) expenseView {
	v := expenseView{ID: e.ID, Date: e.SpentAt.String(), Amount: e.Amount.String(), Category: string(e.Category)}
	if e.Comment != nil {
		v.Comment = *e.Comment
	}
	if !e.CreatedAt.IsZero() {
		v.AddedAt = e.CreatedAt.Format("2006-01-02T15:04Z")
	}
	return v
}

func parseDate(field string, s *string, today dates.Date) (*dates.Date, error) {
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
			return nil, badInput(field, "%s must be %s", field, dateDesc)
		}
		d = p
	}
	return &d, nil
}

func parseAmount(field string, s *string) (*money.Amount, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	a, err := money.ParsePositive(*s)
	if err != nil {
		return nil, badInput(field, "%s: %v", field, err)
	}
	return &a, nil
}

func parseCategory(field string, s *string) (*expense.Category, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	c, err := expense.ParseCategory(*s)
	if err != nil {
		return nil, badInput(field, "%v", err)
	}
	return &c, nil
}

type periodArgs struct {
	Preset *string `json:"preset"`
	From   *string `json:"from"`
	To     *string `json:"to"`
}

// resolve returns the half-open range; def is used when nothing is given
// (nil def means all time).
func (p periodArgs) resolve(today dates.Date, def *dates.Preset) (dates.Range, error) {
	allTime := dates.Range{From: expense.MinSpentAt, To: today.AddDays(1)}
	if p.Preset != nil && strings.TrimSpace(*p.Preset) != "" {
		name := strings.ToLower(strings.TrimSpace(*p.Preset))
		if name == "all_time" {
			return allTime, nil
		}
		r, err := dates.Preset(name).Resolve(today)
		if err != nil {
			return dates.Range{}, badInput("preset", "unknown preset %q", name)
		}
		return r, nil
	}
	from, err := parseDate("from", p.From, today)
	if err != nil {
		return dates.Range{}, err
	}
	to, err := parseDate("to", p.To, today)
	if err != nil {
		return dates.Range{}, err
	}
	if from == nil && to == nil {
		if def == nil {
			return allTime, nil
		}
		return def.Resolve(today)
	}
	r := allTime
	if from != nil {
		r.From = *from
	}
	if to != nil {
		r.To = to.AddDays(1) // inclusive end date
	}
	if !r.Valid() {
		return dates.Range{}, badInput("to", "the end of the period is before its start")
	}
	return r, nil
}

func periodView(r dates.Range) map[string]string {
	return map[string]string{"from": r.From.String(), "to_inclusive": r.LastDay().String()}
}

// validation converts a service validation error into a tool error.
func validation(err error) error {
	var ve *expense.ValidationError
	if errors.As(err, &ve) {
		fields := make([]string, 0, len(ve.Fields))
		for f := range ve.Fields {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		msgs := make([]string, len(fields))
		for i, f := range fields {
			msgs[i] = ve.Fields[f]
		}
		return &toolError{Field: strings.Join(fields, ","), Message: strings.Join(msgs, "; ")}
	}
	if errors.Is(err, expense.ErrNotFound) {
		return badInput("id", "expense not found among the user's expenses")
	}
	return err
}

func (tb *toolbox) checkMutationBudget() error {
	if len(tb.changes) >= maxMutations {
		return badInput("", "too many changes in one message; ask the user to continue in another message")
	}
	return nil
}

func (tb *toolbox) add(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Items []struct {
			Amount   *string `json:"amount"`
			Category *string `json:"category"`
			Date     *string `json:"date"`
			Comment  *string `json:"comment"`
		} `json:"items"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if len(in.Items) == 0 || len(in.Items) > maxAddItems {
		return nil, badInput("items", "items must contain 1 to %d expenses", maxAddItems)
	}
	if len(tb.changes)+len(in.Items) > maxMutations {
		return nil, badInput("items", "too many changes in one message; ask the user to continue in another message")
	}
	// Validate everything before saving anything.
	fields := make([]expense.Fields, len(in.Items))
	for i, it := range in.Items {
		prefix := fmt.Sprintf("items[%d].", i)
		amount, err := parseAmount(prefix+"amount", it.Amount)
		if err != nil {
			return nil, err
		}
		if amount == nil {
			return nil, badInput(prefix+"amount", "amount is required; ask the user for it")
		}
		cat, err := parseCategory(prefix+"category", it.Category)
		if err != nil {
			return nil, err
		}
		if cat == nil {
			return nil, badInput(prefix+"category", "category is required")
		}
		day, err := parseDate(prefix+"date", it.Date, tb.today)
		if err != nil {
			return nil, err
		}
		f := expense.Fields{UserID: tb.actor, Amount: *amount, Category: *cat, SpentAt: tb.today, Comment: it.Comment}
		if day != nil {
			f.SpentAt = *day
		}
		if fields[i], err = tb.svc.ValidateFields(tb.scope(), f); err != nil {
			err = validation(err)
			var te *toolError
			if errors.As(err, &te) {
				te.Field = prefix + te.Field
			}
			return nil, err
		}
	}
	created := make([]expenseView, 0, len(fields))
	for _, f := range fields {
		e, err := tb.svc.Create(ctx, tb.scope(), f)
		if err != nil {
			return nil, validation(err)
		}
		tb.changes = append(tb.changes, Change{Kind: Created, After: e})
		tb.remember(e.ID)
		created = append(created, view(e))
	}
	return map[string]any{"created": created, "currency": tb.currency}, nil
}

func (tb *toolbox) find(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		periodArgs
		Category *string `json:"category"`
		Amount   *string `json:"amount"`
		Text     *string `json:"text"`
		Sort     string  `json:"sort"`
		Limit    *int    `json:"limit"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	period, err := in.periodArgs.resolve(tb.today, nil)
	if err != nil {
		return nil, err
	}
	f := expense.Filter{Period: &period, Order: expense.OrderSpentDesc, Limit: defaultFindRows}
	if in.Sort == "recently_added" {
		f.Order = expense.OrderCreatedDesc
	}
	if in.Limit != nil {
		f.Limit = min(max(*in.Limit, 1), maxFindLimit)
	}
	if f.Category, err = parseCategory("category", in.Category); err != nil {
		return nil, err
	}
	if f.Amount, err = parseAmount("amount", in.Amount); err != nil {
		return nil, err
	}
	if in.Text != nil {
		f.TextContains = strings.TrimSpace(*in.Text)
	}
	page, err := tb.svc.List(ctx, tb.scope(), f)
	if err != nil {
		return nil, validation(err)
	}
	items := make([]expenseView, len(page.Items))
	for i, e := range page.Items {
		tb.remember(e.ID)
		items[i] = view(e)
	}
	return map[string]any{
		"period": periodView(period), "total_count": page.Total, "total_sum": page.Sum.String(),
		"currency": tb.currency, "items": items, "more_not_shown": page.Total - len(items),
	}, nil
}

func (tb *toolbox) summarize(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		periodArgs
		Category *string `json:"category"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	month := dates.ThisMonth
	period, err := in.periodArgs.resolve(tb.today, &month)
	if err != nil {
		return nil, err
	}
	cat, err := parseCategory("category", in.Category)
	if err != nil {
		return nil, err
	}
	total, err := tb.svc.List(ctx, tb.scope(), expense.Filter{Period: &period, Category: cat, Limit: 1})
	if err != nil {
		return nil, validation(err)
	}
	out := map[string]any{
		"period": periodView(period), "total_count": total.Total, "total_sum": total.Sum.String(), "currency": tb.currency,
	}
	if cat != nil {
		out["category"] = string(*cat)
		return out, nil
	}
	var breakdown []map[string]any
	for _, c := range expense.Categories {
		c := c
		p, err := tb.svc.List(ctx, tb.scope(), expense.Filter{Period: &period, Category: &c, Limit: 1})
		if err != nil {
			return nil, validation(err)
		}
		if p.Total > 0 {
			breakdown = append(breakdown, map[string]any{"category": string(c), "count": p.Total, "sum": p.Sum.String()})
		}
	}
	out["by_category"] = breakdown
	return out, nil
}

func (tb *toolbox) target(ctx context.Context, id int64) (expense.Expense, error) {
	if !tb.seen[id] {
		return expense.Expense{}, badInput("id", "unknown id %d: find the expense with find_expenses first and use an id from its result", id)
	}
	e, err := tb.svc.Get(ctx, tb.scope(), id)
	if err != nil {
		return expense.Expense{}, validation(err)
	}
	return e, nil
}

func (tb *toolbox) update(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		ID       int64   `json:"id"`
		Amount   *string `json:"amount"`
		Category *string `json:"category"`
		Date     *string `json:"date"`
		Comment  *string `json:"comment"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := tb.checkMutationBudget(); err != nil {
		return nil, err
	}
	before, err := tb.target(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	f := expense.Fields{UserID: tb.actor, Amount: before.Amount, Category: before.Category, SpentAt: before.SpentAt, Comment: before.Comment}
	changed := false
	if a, err := parseAmount("amount", in.Amount); err != nil {
		return nil, err
	} else if a != nil {
		f.Amount, changed = *a, true
	}
	if c, err := parseCategory("category", in.Category); err != nil {
		return nil, err
	} else if c != nil {
		f.Category, changed = *c, true
	}
	if d, err := parseDate("date", in.Date, tb.today); err != nil {
		return nil, err
	} else if d != nil {
		f.SpentAt, changed = *d, true
	}
	if in.Comment != nil {
		f.Comment, changed = in.Comment, true
	}
	if !changed {
		return nil, badInput("", "no new values given")
	}
	after, err := tb.svc.Update(ctx, tb.scope(), in.ID, f)
	if err != nil {
		return nil, validation(err)
	}
	tb.changes = append(tb.changes, Change{Kind: Updated, Before: before, After: after})
	return map[string]any{"before": view(before), "after": view(after), "currency": tb.currency}, nil
}

func (tb *toolbox) delete(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := tb.checkMutationBudget(); err != nil {
		return nil, err
	}
	before, err := tb.target(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := tb.svc.Delete(ctx, tb.scope(), in.ID); err != nil {
		return nil, validation(err)
	}
	tb.changes = append(tb.changes, Change{Kind: Deleted, Before: before})
	return map[string]any{"deleted": view(before), "currency": tb.currency}, nil
}
