// Package bot handles Telegram updates: it authorises the sender, asks the
// LLM parser for an intent, and executes that intent through the expense
// service strictly within the sender's own scope.
//
// Security model:
//   - The actor is taken only from the Telegram update (message.from.id).
//   - Only configured family members are served, and only in private chats.
//   - Every read and write uses expense.OwnerScope(actor); the parser output
//     has no field that can name another owner.
//   - Update ids are claimed in the same transaction as the operation, so a
//     redelivered update never applies twice.
//   - The reply is sent only after the transaction commits.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/llm"
	"expensetracker/internal/telegram"
)

// Config holds bot settings.
type Config struct {
	Currency   string
	Language   string
	LLMTimeout time.Duration
}

// Bot processes updates.
type Bot struct {
	svc    *expense.Service
	parser llm.Parser
	sender telegram.Sender
	msgs   Messages
	cfg    Config
	log    *slog.Logger
}

// New builds a bot.
func New(svc *expense.Service, parser llm.Parser, sender telegram.Sender, cfg Config, log *slog.Logger) *Bot {
	if cfg.LLMTimeout <= 0 {
		cfg.LLMTimeout = 30 * time.Second
	}
	return &Bot{svc: svc, parser: parser, sender: sender, msgs: Catalog(cfg.Language), cfg: cfg, log: log}
}

var errDuplicate = errors.New("update already processed")

// HandleUpdate processes one update end to end. It returns an error only for
// failures worth logging; user-facing outcomes are sent as replies.
func (b *Bot) HandleUpdate(ctx context.Context, u telegram.Update) error {
	msg := u.Message
	if msg == nil || msg.From == nil || msg.From.IsBot {
		return nil
	}
	actor := msg.From.ID
	if msg.Chat.Type != "private" {
		return b.reply(ctx, msg.Chat.ID, b.msgs.PrivateOnly)
	}
	if !b.svc.IsMember(actor) {
		b.log.Info("rejected message from non-allowlisted user", "update_id", u.UpdateID)
		return b.reply(ctx, msg.Chat.ID, fmt.Sprintf(b.msgs.NotAllowed, actor))
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return b.reply(ctx, msg.Chat.ID, b.msgs.TextOnly)
	}
	if text == "/start" || text == "/help" {
		return b.reply(ctx, msg.Chat.ID, b.msgs.Help)
	}

	// Cheap pre-check so a redelivered update does not trigger another LLM
	// call; the authoritative check is the claim inside the transaction.
	if done, err := b.svc.IsUpdateClaimed(ctx, u.UpdateID); err != nil {
		b.log.Error("idempotency check failed", "update_id", u.UpdateID, "error", err.Error())
		return b.reply(ctx, msg.Chat.ID, b.msgs.StorageFailed)
	} else if done {
		b.log.Info("skipping duplicate update", "update_id", u.UpdateID)
		return nil
	}

	pctx, cancel := context.WithTimeout(ctx, b.cfg.LLMTimeout)
	intent, err := b.parser.Parse(pctx, llm.Request{
		Text: text, Today: b.svc.Today(), TimeZone: b.svc.Location().String(), Language: b.cfg.Language,
	})
	cancel()
	if err != nil {
		var fe *llm.FieldError
		switch {
		case errors.As(err, &fe):
			return b.reply(ctx, msg.Chat.ID, b.fieldMessage(fe.Field))
		case errors.Is(err, llm.ErrMalformed):
			b.log.Warn("llm returned malformed intent", "update_id", u.UpdateID)
			return b.reply(ctx, msg.Chat.ID, b.msgs.LLMMalformed)
		default:
			b.log.Warn("llm unavailable", "update_id", u.UpdateID, "error", err.Error())
			return b.reply(ctx, msg.Chat.ID, b.msgs.LLMUnavailable)
		}
	}

	var reply string
	err = b.svc.InTx(ctx, func(tx *expense.Service) error {
		claimed, err := tx.ClaimUpdate(ctx, u.UpdateID, actor)
		if err != nil {
			return err
		}
		if !claimed {
			return errDuplicate
		}
		reply, err = b.execute(ctx, tx, actor, intent)
		return err
	})
	switch {
	case errors.Is(err, errDuplicate):
		b.log.Info("skipping duplicate update", "update_id", u.UpdateID)
		return nil
	case err != nil:
		b.log.Error("operation failed", "update_id", u.UpdateID, "action", string(intent.Action), "error", err.Error())
		return b.reply(ctx, msg.Chat.ID, b.msgs.StorageFailed)
	}
	b.log.Info("update processed", "update_id", u.UpdateID, "action", string(intent.Action))
	return b.reply(ctx, msg.Chat.ID, reply)
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) error {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := b.sender.SendMessage(sctx, chatID, text); err != nil {
		return fmt.Errorf("send reply: %w", err)
	}
	return nil
}

func (b *Bot) fieldMessage(field string) string {
	switch {
	case strings.HasSuffix(field, "amount"):
		return b.msgs.InvalidAmount
	case strings.HasSuffix(field, "date") || field == "spent_at" || strings.HasPrefix(field, "period."):
		return b.msgs.InvalidDate
	case field == "period":
		return b.msgs.InvalidPeriod
	case field == "comment":
		return b.msgs.InvalidComment
	}
	return b.msgs.Default
}

// validationReply converts a service validation error into a reply; other
// errors are returned for the caller to roll back.
func (b *Bot) validationReply(err error) (string, error) {
	var ve *expense.ValidationError
	if errors.As(err, &ve) {
		for _, f := range []string{"amount", "spent_at", "comment", "category"} {
			if _, ok := ve.Fields[f]; ok {
				if f == "category" {
					return fmt.Sprintf(b.msgs.AskCategory, b.msgs.categoryList()), nil
				}
				return b.fieldMessage(f), nil
			}
		}
		return b.msgs.Default, nil
	}
	return "", err
}

// execute runs the intent inside the transaction-bound service. All access is
// scoped to actor.
func (b *Bot) execute(ctx context.Context, svc *expense.Service, actor int64, in llm.Intent) (string, error) {
	scope := expense.OwnerScope(actor)
	switch in.Action {
	case llm.ActionCreate:
		if in.Amount == nil {
			return b.msgs.AskAmount, nil
		}
		if in.Category == nil {
			return fmt.Sprintf(b.msgs.AskCategory, b.msgs.categoryList()), nil
		}
		day := svc.Today()
		if in.Date != nil {
			day = *in.Date
		}
		e, err := svc.Create(ctx, scope, expense.Fields{UserID: actor, Amount: *in.Amount, Category: *in.Category, SpentAt: day, Comment: in.Comment})
		if err != nil {
			return b.validationReply(err)
		}
		return fmt.Sprintf(b.msgs.Created, b.msgs.expenseLine(e, b.cfg.Currency)), nil

	case llm.ActionList, llm.ActionSummary:
		period := in.Period
		if period == nil {
			r, _ := dates.ThisMonth.Resolve(svc.Today())
			period = &r
		}
		const shown = 20
		f := expense.Filter{Period: period, Category: in.Category, Order: expense.OrderSpentDesc, Limit: shown}
		if in.Action == llm.ActionSummary {
			f.Limit = 1
		}
		page, err := svc.List(ctx, scope, f)
		if err != nil {
			return b.validationReply(err)
		}
		desc := b.msgs.period(*period)
		if in.Category != nil {
			desc = fmt.Sprintf(b.msgs.InCategory, b.msgs.Categories[*in.Category]) + " " + desc
		}
		if in.Action == llm.ActionSummary {
			return fmt.Sprintf(b.msgs.Summary, desc, page.Sum.Human(), b.cfg.Currency, page.Total), nil
		}
		if page.Total == 0 {
			return fmt.Sprintf(b.msgs.ListEmpty, desc), nil
		}
		lines := []string{fmt.Sprintf(b.msgs.ListHeader, desc)}
		for _, e := range page.Items {
			lines = append(lines, "• "+b.msgs.expenseLine(e, b.cfg.Currency))
		}
		if page.Total > len(page.Items) {
			lines = append(lines, fmt.Sprintf(b.msgs.ListTruncated, len(page.Items)))
		}
		lines = append(lines, fmt.Sprintf(b.msgs.ListFooter, page.Total, page.Sum.Human(), b.cfg.Currency))
		return strings.Join(lines, "\n"), nil

	case llm.ActionUpdate:
		if in.Changes.Empty() {
			return b.msgs.AskWhatChange, nil
		}
		target, reply, err := b.resolveTarget(ctx, svc, scope, in.Target)
		if err != nil || reply != "" {
			return reply, err
		}
		f := expense.Fields{UserID: actor, Amount: target.Amount, Category: target.Category, SpentAt: target.SpentAt, Comment: target.Comment}
		if in.Changes.Amount != nil {
			f.Amount = *in.Changes.Amount
		}
		if in.Changes.Category != nil {
			f.Category = *in.Changes.Category
		}
		if in.Changes.Date != nil {
			f.SpentAt = *in.Changes.Date
		}
		if in.Changes.Comment != nil {
			f.Comment = in.Changes.Comment
		}
		updated, err := svc.Update(ctx, scope, target.ID, f)
		if err != nil {
			return b.validationReply(err)
		}
		return fmt.Sprintf(b.msgs.Updated, b.msgs.expenseLine(target, b.cfg.Currency), b.msgs.expenseLine(updated, b.cfg.Currency)), nil

	case llm.ActionDelete:
		target, reply, err := b.resolveTarget(ctx, svc, scope, in.Target)
		if err != nil || reply != "" {
			return reply, err
		}
		if err := svc.Delete(ctx, scope, target.ID); err != nil {
			if errors.Is(err, expense.ErrNotFound) {
				return b.msgs.NotFound, nil
			}
			return "", err
		}
		return fmt.Sprintf(b.msgs.Deleted, b.msgs.expenseLine(target, b.cfg.Currency)), nil

	case llm.ActionClarify:
		if in.Question != "" {
			return in.Question, nil
		}
		return b.msgs.Default, nil
	}
	return b.msgs.Help, nil
}

// resolveTarget finds exactly one of the actor's expenses. It returns a
// non-empty reply (and no expense) when the target is missing or ambiguous;
// in that case the caller must not mutate anything.
func (b *Bot) resolveTarget(ctx context.Context, svc *expense.Service, scope expense.Scope, t llm.Target) (expense.Expense, string, error) {
	if !t.HasCriteria() {
		return expense.Expense{}, b.msgs.AskWhichOne, nil
	}
	if t.Last {
		page, err := svc.List(ctx, scope, expense.Filter{Order: expense.OrderCreatedDesc, Limit: 1})
		if err != nil {
			return expense.Expense{}, "", err
		}
		if len(page.Items) == 0 {
			return expense.Expense{}, b.msgs.NotFound, nil
		}
		e := page.Items[0]
		if !matches(e, t) {
			return expense.Expense{}, fmt.Sprintf(b.msgs.LastMismatch, b.msgs.expenseLine(e, b.cfg.Currency)), nil
		}
		return e, "", nil
	}

	f := expense.Filter{Amount: t.Amount, Category: t.Category, TextContains: t.Text, Order: expense.OrderCreatedDesc, Limit: 5}
	if t.Date != nil {
		r := dates.Range{From: *t.Date, To: t.Date.AddDays(1)}
		f.Period = &r
	}
	page, err := svc.List(ctx, scope, f)
	if err != nil {
		return expense.Expense{}, "", err
	}
	switch {
	case page.Total == 0:
		return expense.Expense{}, b.msgs.NotFound, nil
	case page.Total > 1:
		lines := []string{fmt.Sprintf(b.msgs.Ambiguous, page.Total)}
		for _, e := range page.Items {
			lines = append(lines, "• "+b.msgs.expenseLine(e, b.cfg.Currency))
		}
		if page.Total > len(page.Items) {
			lines = append(lines, fmt.Sprintf(b.msgs.AndMore, page.Total-len(page.Items)))
		}
		return expense.Expense{}, strings.Join(lines, "\n"), nil
	}
	return page.Items[0], "", nil
}

func matches(e expense.Expense, t llm.Target) bool {
	if t.Amount != nil && e.Amount != *t.Amount {
		return false
	}
	if t.Category != nil && e.Category != *t.Category {
		return false
	}
	if t.Date != nil && !e.SpentAt.Equal(*t.Date) {
		return false
	}
	if t.Text != "" && (e.Comment == nil || !strings.Contains(strings.ToLower(*e.Comment), strings.ToLower(t.Text))) {
		return false
	}
	return true
}

// Poller runs the long-polling loop.
type Poller interface {
	GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]telegram.Update, error)
}

// Run polls Telegram until ctx is cancelled. Updates are processed one at a
// time; the offset is advanced after each update is handled so Telegram
// redelivers anything not yet handled after a restart (and the durable claim
// makes that redelivery harmless).
func (b *Bot) Run(ctx context.Context, p Poller, heartbeatFile string) error {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		touch(heartbeatFile)
		ups, err := p.GetUpdates(ctx, offset, 25)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			var apiErr *telegram.APIError
			if errors.As(err, &apiErr) && apiErr.Code == 409 {
				b.log.Error("telegram getUpdates conflict: another bot instance or a webhook is active for this token")
			} else {
				b.log.Warn("telegram getUpdates failed", "error", err.Error())
			}
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			if err := b.HandleUpdate(ctx, u); err != nil {
				b.log.Warn("update handling incomplete", "update_id", u.UpdateID, "error", err.Error())
			}
			offset = u.UpdateID + 1
		}
	}
	return nil
}

func touch(path string) {
	if path == "" {
		return
	}
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		if f, err := os.Create(path); err == nil {
			_ = f.Close()
		}
	}
}
