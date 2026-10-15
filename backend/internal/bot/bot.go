// Package bot handles Telegram updates: it authorises the sender, loads the
// short conversation memory, and runs the AI agent for the message inside one
// database transaction scoped to the sender.
//
// Security model:
//   - The actor is taken only from the Telegram update (message.from.id).
//   - Only configured family members are served, and only in private chats;
//     the LLM is never called for anyone else.
//   - The agent's tools act only on expense.OwnerScope(actor); no tool
//     argument can name another owner.
//   - The update id is claimed in the same transaction as the agent's
//     changes and the memory write, so a redelivered update never applies
//     twice and a failed turn leaves nothing behind.
//   - The reply is sent only after the transaction commits, and its receipt
//     of changes comes from what the tools actually did.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"expensetracker/internal/agent"
	"expensetracker/internal/expense"
	"expensetracker/internal/llm"
	"expensetracker/internal/telegram"
)

// Config holds bot settings.
type Config struct {
	Currency string
	Language string
	// Conversation memory: messages newer than MemoryWindow, at most
	// MemoryMessages of them and MemoryChars characters, are given to the
	// agent. Stored messages are deleted after MemoryRetention.
	MemoryWindow    time.Duration
	MemoryMessages  int
	MemoryChars     int
	MemoryRetention time.Duration
	// OnTurn, if set, is called after every agent turn (for evals/metrics).
	OnTurn func(TurnInfo)
}

// TurnInfo describes one processed message.
type TurnInfo struct {
	UpdateID int64
	Stats    agent.Stats
	Duration time.Duration
	Err      error
}

// Bot processes updates.
type Bot struct {
	svc    *expense.Service
	agent  *agent.Agent
	sender telegram.Sender
	msgs   Messages
	cfg    Config
	log    *slog.Logger
}

// New builds a bot.
func New(svc *expense.Service, ag *agent.Agent, sender telegram.Sender, cfg Config, log *slog.Logger) *Bot {
	if cfg.MemoryWindow <= 0 {
		cfg.MemoryWindow = 6 * time.Hour
	}
	if cfg.MemoryMessages <= 0 {
		cfg.MemoryMessages = 12
	}
	if cfg.MemoryChars <= 0 {
		cfg.MemoryChars = 6000
	}
	if cfg.MemoryRetention <= 0 {
		cfg.MemoryRetention = 7 * 24 * time.Hour
	}
	return &Bot{svc: svc, agent: ag, sender: sender, msgs: Catalog(cfg.Language), cfg: cfg, log: log}
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

	now := b.svc.Now()
	recent, err := b.svc.RecentChat(ctx, actor, now.Add(-b.cfg.MemoryWindow), b.cfg.MemoryMessages)
	if err != nil {
		b.log.Error("loading conversation failed", "update_id", u.UpdateID, "error", err.Error())
		return b.reply(ctx, msg.Chat.ID, b.msgs.StorageFailed)
	}
	history, knownIDs := agent.History(recent, b.cfg.MemoryChars)

	// The whole agent turn runs in one transaction: model calls happen while
	// it is open, which is fine for a family-sized bot that handles updates
	// one at a time, and it means a failure at any step saves nothing.
	start := time.Now()
	var res agent.Result
	var reply string
	err = b.svc.InTx(ctx, func(tx *expense.Service) error {
		claimed, err := tx.ClaimUpdate(ctx, u.UpdateID, actor)
		if err != nil {
			return err
		}
		if !claimed {
			return errDuplicate
		}
		res, err = b.agent.Run(ctx, tx, actor, history, knownIDs, text)
		if err != nil {
			return err
		}
		reply = b.compose(res)
		return tx.AppendChat(ctx, actor, []expense.ChatMessage{
			{Role: expense.ChatUser, Content: text},
			{Role: expense.ChatAssistant, Content: reply + agent.RefsNote(res.Refs)},
		}, now.Add(-b.cfg.MemoryRetention))
	})
	if b.cfg.OnTurn != nil && !errors.Is(err, errDuplicate) {
		b.cfg.OnTurn(TurnInfo{UpdateID: u.UpdateID, Stats: res.Stats, Duration: time.Since(start), Err: err})
	}
	logAttrs := []any{"update_id", u.UpdateID, "steps", res.Stats.Steps, "tools", strings.Join(res.Stats.ToolCalls, ","),
		"tool_errors", res.Stats.ToolErrors, "input_tokens", res.Stats.Usage.InputTokens, "output_tokens", res.Stats.Usage.OutputTokens,
		"duration_ms", time.Since(start).Milliseconds()}
	switch {
	case errors.Is(err, errDuplicate):
		b.log.Info("skipping duplicate update", "update_id", u.UpdateID)
		return nil
	case errors.Is(err, llm.ErrMalformed):
		b.log.Warn("llm returned an unusable answer", append(logAttrs, "error", err.Error())...)
		return b.reply(ctx, msg.Chat.ID, b.msgs.LLMMalformed)
	case errors.Is(err, llm.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded):
		b.log.Warn("llm unavailable", append(logAttrs, "error", err.Error())...)
		return b.reply(ctx, msg.Chat.ID, b.msgs.LLMUnavailable)
	case errors.Is(err, agent.ErrStepLimit):
		b.log.Warn("agent step limit reached", logAttrs...)
		return b.reply(ctx, msg.Chat.ID, b.msgs.StepLimit)
	case err != nil:
		b.log.Error("operation failed", append(logAttrs, "error", err.Error())...)
		return b.reply(ctx, msg.Chat.ID, b.msgs.StorageFailed)
	}
	b.log.Info("update processed", append(logAttrs, "changes", len(res.Changes))...)
	return b.reply(ctx, msg.Chat.ID, reply)
}

// compose builds the reply: the agent's text followed by the receipt of
// changes. With neither, the help text is shown.
func (b *Bot) compose(res agent.Result) string {
	var parts []string
	if res.Text != "" {
		parts = append(parts, res.Text)
	}
	if lines := b.msgs.receipt(res.Changes, b.cfg.Currency); len(lines) > 0 {
		parts = append(parts, strings.Join(lines, "\n"))
	}
	if len(parts) == 0 {
		return b.msgs.Help
	}
	return strings.Join(parts, "\n\n")
}

// replyBackoff is the pause before each retry of a failed reply.
var replyBackoff = []time.Duration{time.Second, 3 * time.Second}

// reply sends a message, retrying transient failures: the reply is sent after
// the transaction committed, so a dropped connection would otherwise lose the
// answer to a change that was saved (seen live: a timeout followed by EOF).
// Telegram's 4xx answers other than 429 are permanent and not retried. A
// timed-out attempt may still have been delivered, so a retry can rarely
// duplicate a message; that is better than losing it.
func (b *Bot) reply(ctx context.Context, chatID int64, text string) error {
	ctx = context.WithoutCancel(ctx)
	var err error
	for attempt := 0; ; attempt++ {
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = b.sender.SendMessage(sctx, chatID, text)
		cancel()
		if err == nil {
			return nil
		}
		var apiErr *telegram.APIError
		if errors.As(err, &apiErr) && apiErr.Code >= 400 && apiErr.Code < 500 && apiErr.Code != 429 {
			break
		}
		if attempt >= len(replyBackoff) {
			break
		}
		b.log.Warn("reply failed, retrying", "attempt", attempt+1, "error", err.Error())
		time.Sleep(replyBackoff[attempt])
	}
	return fmt.Errorf("send reply: %w", err)
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
		Touch(heartbeatFile)
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

// Touch updates the heartbeat file read by the container health check.
func Touch(path string) {
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
