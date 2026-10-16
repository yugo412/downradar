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

	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/config"
	"downradar.info/backend/internal/httpapi"
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

	profiles, err := catalog.Load(cfg.ServicesFile)
	if err != nil {
		return err
	}
	log.Info("profiles loaded", "profiles", len(profiles), "file", cfg.ServicesFile)

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

	p := poller.New(st, profiles, cfg.PollConcurrency, cfg.PollTick, log)
	go p.Run(ctx)

	if cfg.RawPayloadDays > 0 {
		go runRawPayloadCleanup(ctx, st, time.Duration(cfg.RawPayloadDays)*24*time.Hour, log)
	}

	go runSessionCleanup(ctx, st, log)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(st, log, httpapi.Options{
			RateLimitPerMinute:  cfg.RateLimitPerMinute,
			RateLimitBurst:      cfg.RateLimitBurst,
			SuggestPerHour:      cfg.SuggestPerHour,
			SuggestBurst:        cfg.SuggestBurst,
			LoginPerHour:        cfg.LoginPerHour,
			LoginBurst:          cfg.LoginBurst,
			PasswordPerHour:     cfg.PasswordPerHour,
			PasswordBurst:       cfg.PasswordBurst,
			InternalToken:       cfg.InternalToken,
			Profiles:            profiles,
			SessionCookie:       cfg.SessionCookie,
			SessionTTL:          cfg.SessionTTL,
			SessionCookieSecure: cfg.SessionCookieSecure,
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

func runSessionCleanup(ctx context.Context, st *store.Store, log *slog.Logger) {
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
