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
	_ "downradar.info/backend/internal/adapters/all"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/config"
	"downradar.info/backend/internal/contact"
	"downradar.info/backend/internal/httpapi"
	"downradar.info/backend/internal/mail"
	"downradar.info/backend/internal/notify"
	"downradar.info/backend/internal/oauth"
	"downradar.info/backend/internal/poller"
	"downradar.info/backend/internal/slack"
	"downradar.info/backend/internal/store"
	"downradar.info/backend/internal/summary"
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

	if cfg.SMTPAddr != "" && cfg.AppKey == "" {
		return errors.New("APP_KEY is required while alerts are enabled")
	}

	st, err := store.New(ctx, cfg.DatabaseURL, store.Options{
		RawPayloadMinInterval: cfg.RawPayloadMinInterval,
		DefaultLimits: map[string]int{
			store.LimitAlert: cfg.AlertLimitPerSubscriber,
		},
		AppKey:           cfg.AppKey,
		DisableSummaries: !cfg.SummaryEnabled,
	})
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	log.Info("migrations applied")

	if cfg.AppKey != "" {
		if err := st.SealLegacySettings(ctx); err != nil {
			return err
		}
		log.Info("channel settings sealed")
	}

	p := poller.New(st, cat, cfg.PollConcurrency, cfg.PollTick, log)
	if summaries := newSummaryGenerator(cfg, st, log); summaries != nil {
		p.WithSummarizer(summaries)
		go summaries.Run(ctx)
		log.Info("generated summaries are on", "model", cfg.OpenAIModel)
	} else if !cfg.SummaryEnabled {
		log.Info("generated summaries are off: SUMMARY_ENABLED is false")
	} else {
		log.Warn("generated summaries are off: OPENAI_API_KEY is not set")
	}
	go p.Run(ctx)

	var accountMailer *account.Mailer
	var contactMailer *contact.Mailer
	var notifier *notify.Notifier
	if cfg.SMTPAddr != "" {
		sender := mail.NewSMTP(cfg.SMTPAddr, cfg.MailFrom, cfg.SMTPUser, cfg.SMTPPassword, 0)
		accountMailer = account.New(sender, cfg.PublicURL)
		contactMailer = contact.New(sender, cfg.ContactEmail)

		notifier = notify.New(st, sender, cfg.PublicURL, cfg.AlertSendInterval, log).
			WithTelegram(cfg.TelegramAPIBase)
		go notifier.Run(ctx)
	} else {
		log.Warn("mail is off: SMTP_ADDR is not set, so no email is sent, alerts cannot be subscribed to, the contact form is refused, and no account can be made from the site")
	}

	go runAuthCleanup(ctx, st, log)

	// Signing in with a provider is off until both halves of its credentials are
	// set, the way mail is off without SMTP_ADDR: the screens do not offer the
	// button and the routes say the provider is not configured.
	providers := map[string]oauth.Provider{}
	if cfg.GitHubClientID != "" && cfg.GitHubClientSecret != "" {
		providers["github"] = oauth.NewGitHub(cfg.GitHubClientID, cfg.GitHubClientSecret)
	} else {
		log.Warn("sign in with github is off: GITHUB_CLIENT_ID or GITHUB_CLIENT_SECRET is not set")
	}
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" {
		providers["google"] = oauth.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret)
	} else {
		log.Warn("sign in with google is off: GOOGLE_CLIENT_ID or GOOGLE_CLIENT_SECRET is not set")
	}

	if cfg.RawPayloadDays > 0 {
		go runRawPayloadCleanup(ctx, st, time.Duration(cfg.RawPayloadDays)*24*time.Hour, log)
	}

	// Adding a Slack channel in one click is the site's own Slack app. Without
	// both halves of its credentials the page keeps the manual webhook path and
	// does not offer the button.
	var slackApp *slack.App
	if cfg.SlackClientID != "" && cfg.SlackClientSecret != "" {
		slackApp = slack.NewApp(cfg.SlackClientID, cfg.SlackClientSecret)
	} else {
		log.Warn("adding to slack is off: SLACK_CLIENT_ID or SLACK_CLIENT_SECRET is not set")
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
			AlertTestPerHour:        cfg.AlertTestPerHour,
			AlertTestBurst:          cfg.AlertTestBurst,
			AlertLimitPerSubscriber: cfg.AlertLimitPerSubscriber,
			AccountPerHour:          cfg.AccountPerHour,
			AccountBurst:            cfg.AccountBurst,
			ContactPerHour:          cfg.ContactPerHour,
			ContactBurst:            cfg.ContactBurst,
			LoginPerHour:            cfg.LoginPerHour,
			LoginBurst:              cfg.LoginBurst,
			LinkPerHour:             cfg.LinkPerHour,
			LinkBurst:               cfg.LinkBurst,
			PasswordPerHour:         cfg.PasswordPerHour,
			PasswordBurst:           cfg.PasswordBurst,
			InternalToken:           cfg.InternalToken,
			Catalog:                 cat,
			AccountMailer:           accountMailer,
			ContactMailer:           contactMailer,
			Notifier:                notifier,
			SessionCookie:           cfg.SessionCookie,
			SessionTTL:              cfg.SessionTTL,
			SessionCookieSecure:     cfg.SessionCookieSecure,
			Providers:               providers,
			Slack:                   slackApp,
			PublicURL:               cfg.PublicURL,
			AlertsEnabled:           cfg.SMTPAddr != "",
			// Telegram is offered only while the sender loop above can drain what
			// it writes: a chat accepted but never sent is what the site refuses.
			// The bot is the subscriber's, so nothing about it is configured here.
			TelegramEnabled: cfg.SMTPAddr != "",
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

func newSummaryGenerator(cfg config.Config, st *store.Store, log *slog.Logger) *summary.Generator {
	if !cfg.SummaryEnabled || cfg.OpenAIAPIKey == "" {
		return nil
	}
	return summary.New(st, summary.Config{
		APIKey:  cfg.OpenAIAPIKey,
		BaseURL: cfg.OpenAIBaseURL,
		Model:   cfg.OpenAIModel,
		Timeout: cfg.OpenAITimeout,
	}, log)
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
