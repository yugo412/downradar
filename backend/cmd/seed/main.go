package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/config"
	"downradar.info/backend/internal/seed"
	"downradar.info/backend/internal/store"
)

func main() {
	file := flag.String("file", "seed/vendors.yaml", "vendor bootstrap file")
	dryRun := flag.Bool("dry-run", false, "validate and report without writing")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*file, *dryRun, log); err != nil {
		log.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run(file string, dryRun bool, log *slog.Logger) error {
	cfg := config.Load()

	profiles, err := catalog.Load(cfg.ServicesFile)
	if err != nil {
		return err
	}
	log.Info("profiles loaded", "profiles", len(profiles), "file", cfg.ServicesFile)

	targets, err := seed.Load(file, profiles)
	if err != nil {
		return err
	}
	log.Info("vendors parsed", "vendors", len(targets), "file", file)

	if dryRun {
		log.Info("dry run, nothing was written")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	st, err := store.New(ctx, cfg.DatabaseURL, store.Options{})
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}

	written, err := st.SeedServices(ctx, targets)
	if err != nil {
		return err
	}
	log.Info("seeded", "vendors", written)
	return nil
}
