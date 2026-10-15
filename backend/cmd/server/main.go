package main

import (
	"context"
	"errors"
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

	targets, err := catalog.Load(cfg.ServicesFile)
	if err != nil {
		return err
	}
	log.Info("catalog loaded", "vendors", len(targets), "file", cfg.ServicesFile)

	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	log.Info("migrations applied")

	ids, err := st.SyncServices(ctx, targets)
	if err != nil {
		return err
	}
	log.Info("services synced", "count", len(ids))

	p := poller.New(st, ids, targets, cfg.PollConcurrency, cfg.PollTick, log)
	go p.Run(ctx)

	go catalog.Watch(ctx, cfg.ServicesFile, cfg.RegistryWatch, func() {
		newTargets, err := catalog.Load(cfg.ServicesFile)
		if err != nil {
			log.Error("registry reload failed, keeping previous configuration", "error", err)
			return
		}
		newIDs, err := st.SyncServices(ctx, newTargets)
		if err != nil {
			log.Error("registry sync failed, keeping previous configuration", "error", err)
			return
		}
		p.Reload(newIDs, newTargets)
		log.Info("registry reloaded", "vendors", len(newTargets))
	})

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(st, log, httpapi.Options{
			RateLimitPerMinute: cfg.RateLimitPerMinute,
			RateLimitBurst:     cfg.RateLimitBurst,
			SuggestPerHour:     cfg.SuggestPerHour,
			SuggestBurst:       cfg.SuggestBurst,
			InternalToken:      cfg.InternalToken,
			GitHubAPIBase:      cfg.GitHubAPIBase,
			GitHubRepo:         cfg.GitHubRepo,
			GitHubToken:        cfg.GitHubToken,
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
