package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"downradar.info/backend/internal/account"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/config"
	"downradar.info/backend/internal/httpapi"
	"downradar.info/backend/internal/mail"
	"downradar.info/backend/internal/notify"
	"downradar.info/backend/internal/poller"
	"downradar.info/backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()

	level := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cat, err := catalog.Load(cfg.ServicesFile)
	if err != nil {
		return err
	}
	log.Info("profiles loaded",
		"profiles", len(cat.Profiles), "detect", len(cat.Detect), "file", cfg.ServicesFile)

	st, err := store.New(ctx, cfg.DatabaseURL, store.Options{
		RawPayloadMinInterval: cfg.RawPayloadMinInterval,
	})
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	log.Info("migrations applied")

	p := poller.New(st, cat, cfg.PollConcurrency, cfg.PollTick, log)
	go p.Run(ctx)

	var accountMailer *account.Mailer
	if cfg.SMTPAddr != "" {
		sender := mail.NewSMTP(cfg.SMTPAddr, cfg.MailFrom, cfg.SMTPUser, cfg.SMTPPassword, 0)
		accountMailer = account.New(sender, cfg.PublicURL)

		n := notify.New(st, sender, cfg.PublicURL, cfg.AlertSendInterval, log)
		go n.Run(ctx)
	} else {
		log.Warn("mail is off: SMTP_ADDR is not set, so no email is sent, alerts cannot be subscribed to, and no account can be made from the site")
	}

	go runAuthCleanup(ctx, st, log)

	if cfg.RawPayloadDays > 0 {
		go runRawPayloadCleanup(ctx, st, time.Duration(cfg.RawPayloadDays)*24*time.Hour, log)
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(st, log, httpapi.Options{
			RateLimitPerMinute:      cfg.RateLimitPerMinute,
			RateLimitBurst:          cfg.RateLimitBurst,
			SuggestPerHour:          cfg.SuggestPerHour,
			SuggestBurst:            cfg.SuggestBurst,
			AlertPerHour:            cfg.AlertPerHour,
			AlertBurst:              cfg.AlertBurst,
			AlertLimitPerSubscriber: cfg.AlertLimitPerSubscriber,
			AccountPerHour:          cfg.AccountPerHour,
			AccountBurst:            cfg.AccountBurst,
			LoginPerHour:            cfg.LoginPerHour,
			LoginBurst:              cfg.LoginBurst,
			PasswordPerHour:         cfg.PasswordPerHour,
			PasswordBurst:           cfg.PasswordBurst,
			InternalToken:           cfg.InternalToken,
			Catalog:                 cat,
			AccountMailer:           accountMailer,
			SessionCookie:           cfg.SessionCookie,
			SessionTTL:              cfg.SessionTTL,
			SessionCookieSecure:     cfg.SessionCookieSecure,
			AlertsEnabled:           cfg.SMTPAddr != "",
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http api listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, stopping gracefully")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func runAuthCleanup(ctx context.Context, st *store.Store, log *slog.Logger) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed, err := st.DeleteExpiredSessions(ctx)
			if err != nil {
				log.Error("session cleanup failed", "error", err)
				continue
			}
			if removed > 0 {
				log.Info("expired sessions removed", "removed", removed)
			}

			tokens, err := st.DeleteExpiredUserTokens(ctx)
			if err != nil {
				log.Error("account link cleanup failed", "error", err)
				continue
			}
			if tokens > 0 {
				log.Info("expired account links removed", "removed", tokens)
			}
		}
	}
}

func runRawPayloadCleanup(ctx context.Context, st *store.Store, age time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed, err := st.DeleteRawPayloadsOlderThan(ctx, age)
			if err != nil {
				log.Error("raw payload cleanup failed", "error", err)
				continue
			}
			if removed > 0 {
				rows, bytes, err := st.RawPayloadStats(ctx)
				if err != nil {
					log.Info("raw payload cleanup", "removed", removed)
					continue
				}
				log.Info("raw payload cleanup",
					"removed", removed, "kept", rows, "size", fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20)))
			}
		}
	}
}
