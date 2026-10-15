// Command expense-tracker runs the family expense service.
//
// Subcommands:
//
//	migrate              apply database migrations and exit
//	api                  serve the REST API
//	bot                  run the Telegram bot (long polling)
//	healthcheck api|bot  container health probe
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"expensetracker/internal/api"
	"expensetracker/internal/bot"
	"expensetracker/internal/config"
	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
	"expensetracker/internal/llm"
	"expensetracker/internal/store/postgres"
	"expensetracker/internal/telegram"
)

const dbWait = 60 * time.Second

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: expense-tracker <migrate|api|bot|healthcheck>")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "migrate":
		err = runMigrate(ctx)
	case "api":
		err = runAPI(ctx)
	case "bot":
		err = runBot(ctx)
	case "healthcheck":
		err = runHealthcheck(os.Args[2:])
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

func runMigrate(ctx context.Context) error {
	cfg, err := config.LoadMigrate()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, dbWait)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool, log); err != nil {
		return err
	}
	log.Info("migrations up to date")
	return nil
}

func runAPI(ctx context.Context) error {
	cfg, err := config.LoadAPI()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, dbWait)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc := expense.NewService(postgres.New(pool), cfg.Members, cfg.Location, dates.SystemClock{})
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(svc, cfg.Currency, cfg.AllowedOrigins, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.Addr, "members", len(cfg.Members), "timezone", cfg.Location.String(), "currency", cfg.Currency)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func runBot(ctx context.Context) error {
	cfg, err := config.LoadBot()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	var backend llm.Completer
	switch cfg.LLM.Provider {
	case "anthropic":
		backend = llm.NewAnthropic(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.BaseURL, cfg.LLM.Timeout)
	case "openai":
		backend = llm.NewOpenAICompatible(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.Timeout)
	case "mock":
		log.Warn("LLM_PROVIDER=mock: using the rule-based development parser. Not for production use.")
		backend = llm.Mock{}
	}

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, dbWait)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc := expense.NewService(postgres.New(pool), cfg.Members, cfg.Location, dates.SystemClock{})

	tg := telegram.NewClient(cfg.TelegramAPIURL, cfg.TelegramToken)
	meCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	_, err = tg.GetMe(meCtx)
	cancel()
	if err != nil {
		var apiErr *telegram.APIError
		if errors.As(err, &apiErr) && apiErr.Code == 401 {
			return errors.New("TELEGRAM_BOT_TOKEN was rejected by Telegram (401 Unauthorized)")
		}
		return fmt.Errorf("cannot reach Telegram: %w", err)
	}

	b := bot.New(svc, llm.ModelParser{Backend: backend}, tg, bot.Config{
		Currency: cfg.Currency, Language: cfg.Language, LLMTimeout: cfg.LLM.Timeout,
	}, log)
	log.Info("bot started", "llm_provider", cfg.LLM.Provider, "llm_model", cfg.LLM.Model, "members", len(cfg.Members), "language", cfg.Language)
	return b.Run(ctx, tg, cfg.HeartbeatFile)
}

func runHealthcheck(args []string) error {
	target := "api"
	if len(args) > 0 {
		target = args[0]
	}
	switch target {
	case "api":
		addr := os.Getenv("HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		if strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + addr + "/healthz")
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("health status %d", resp.StatusCode)
		}
		return nil
	case "bot":
		path := os.Getenv("BOT_HEARTBEAT_FILE")
		if path == "" {
			path = "/tmp/expense-bot-heartbeat"
		}
		st, err := os.Stat(path)
		if err != nil {
			return errors.New("no heartbeat yet")
		}
		if age := time.Since(st.ModTime()); age > 90*time.Second {
			return fmt.Errorf("heartbeat is %s old", age.Round(time.Second))
		}
		return nil
	}
	return fmt.Errorf("unknown healthcheck target %q", target)
}
