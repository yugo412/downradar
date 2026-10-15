package poller

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/store"
)

type Poller struct {
	store       *store.Store
	profiles    catalog.Profiles
	concurrency int
	tick        time.Duration
	log         *slog.Logger
}

// job pairs a stored vendor with the target built from its row and profile.
type job struct {
	row    store.ServiceRow
	target adapters.Target
}

func New(st *store.Store, profiles catalog.Profiles, concurrency int, tick time.Duration, log *slog.Logger) *Poller {
	return &Poller{
		store:       st,
		profiles:    profiles,
		concurrency: concurrency,
		tick:        tick,
		log:         log,
	}
}

func (p *Poller) Run(ctx context.Context) {
	p.log.Info("poller started", "profiles", len(p.profiles), "concurrency", p.concurrency)

	for {
		if err := p.cycle(ctx); err != nil && ctx.Err() == nil {
			p.log.Error("poll cycle failed", "error", err)
		}

		select {
		case <-ctx.Done():
			p.log.Info("poller stopped")
			return
		case <-time.After(p.tick):
		}
	}
}

// cycle reads the vendor list from the table every time. That is what replaced
// the file watcher: a row written by a form is picked up on the next cycle,
// with no reload mechanism in between.
func (p *Poller) cycle(ctx context.Context) error {
	rows, err := p.store.Services(ctx)
	if err != nil {
		return err
	}

	due := dueJobs(p.resolve(rows), time.Now().UTC())
	if len(due) == 0 {
		return nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(p.concurrency)

	for _, item := range due {
		item := item
		g.Go(func() error {
			p.pollOne(gctx, item)
			return nil
		})
	}
	return g.Wait()
}

// resolve turns rows into targets. A row naming a profile that services.yaml no
// longer declares is skipped and logged rather than polled with a wrong adapter.
func (p *Poller) resolve(rows []store.ServiceRow) []job {
	out := make([]job, 0, len(rows))

	for _, row := range rows {
		profile, ok := p.profiles[row.Profile]
		if !ok {
			p.log.Warn("vendor names a profile that is not declared, skipping",
				"vendor", row.Slug, "profile", row.Profile)
			continue
		}

		endpoint := row.Endpoint
		if endpoint == "" {
			endpoint = profile.Endpoint
		}

		out = append(out, job{
			row: row,
			target: adapters.Target{
				Slug:                  row.Slug,
				Name:                  row.Name,
				URL:                   row.URL,
				Category:              row.Category,
				LogoURL:               row.LogoURL,
				Profile:               row.Profile,
				Kind:                  profile.Kind,
				Endpoint:              endpoint,
				Include:               row.Include,
				Aliases:               row.Aliases,
				AllowSharedPage:       row.AllowSharedPage,
				PollIntervalSeconds:   row.PollIntervalSeconds,
				ActiveIntervalSeconds: row.ActiveIntervalSeconds,
				Spec:                  profile.Spec,
			},
		})
	}
	return out
}

// dueJobs picks the vendors that have passed their interval. Vendors that are
// having problems are polled more often.
func dueJobs(jobs []job, now time.Time) []job {
	var due []job
	for _, j := range jobs {
		interval := time.Duration(j.target.PollIntervalSeconds) * time.Second
		if !j.row.Status.Healthy() {
			interval = time.Duration(j.target.ActiveIntervalSeconds) * time.Second
		}
		if j.row.LastPolledAt == nil || now.Sub(*j.row.LastPolledAt) >= interval {
			due = append(due, j)
		}
	}
	return due
}

func (p *Poller) pollOne(ctx context.Context, j job) {
	adapter, ok := adapters.Get(j.target.Kind)
	if !ok {
		p.log.Error("adapter not registered", "vendor", j.target.Slug, "kind", j.target.Kind)
		return
	}

	start := time.Now()
	rep, err := adapter.Fetch(ctx, j.target)
	elapsed := time.Since(start)

	if err != nil {
		if ctx.Err() != nil {
			return
		}
		p.log.Warn("poll failed", "vendor", j.target.Slug, "duration", elapsed.Round(time.Millisecond), "error", err)
		if recErr := p.store.RecordFailure(ctx, j.row.ID, rep.HTTPStatus, err.Error(), int(elapsed.Milliseconds())); recErr != nil {
			p.log.Error("failed to record poll failure", "vendor", j.target.Slug, "error", recErr)
		}
		return
	}

	changed, err := p.store.ApplyReport(ctx, j.row.ID, rep)
	if err != nil {
		p.log.Error("failed to store poll result", "vendor", j.target.Slug, "error", err)
		if recErr := p.store.RecordFailure(ctx, j.row.ID, rep.HTTPStatus, err.Error(), int(elapsed.Milliseconds())); recErr != nil {
			p.log.Error("failed to record the rejected poll", "vendor", j.target.Slug, "error", recErr)
		}
		return
	}

	if changed {
		p.log.Info("status changed",
			"vendor", j.target.Slug,
			"status", rep.Global,
			"components", len(rep.Components),
			"duration", elapsed.Round(time.Millisecond))
	}
}
