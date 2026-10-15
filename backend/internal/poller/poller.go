package poller

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/store"
)

type Poller struct {
	store       *store.Store
	concurrency int
	tick        time.Duration
	log         *slog.Logger

	mu      sync.RWMutex
	ids     map[string]int64
	targets []adapters.Target
}

func New(st *store.Store, ids map[string]int64, targets []adapters.Target,
	concurrency int, tick time.Duration, log *slog.Logger) *Poller {
	return &Poller{
		store:       st,
		ids:         ids,
		targets:     targets,
		concurrency: concurrency,
		tick:        tick,
		log:         log,
	}
}

func (p *Poller) Reload(ids map[string]int64, targets []adapters.Target) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ids = ids
	p.targets = targets
}

func (p *Poller) snapshot() (map[string]int64, []adapters.Target) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ids, p.targets
}

func (p *Poller) Run(ctx context.Context) {
	_, targets := p.snapshot()
	p.log.Info("poller started", "vendors", len(targets), "concurrency", p.concurrency)

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

func (p *Poller) cycle(ctx context.Context) error {
	_, targets := p.snapshot()

	rt, err := p.store.Runtime(ctx)
	if err != nil {
		return err
	}

	due := p.dueTargets(targets, rt, time.Now().UTC())
	if len(due) == 0 {
		return nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(p.concurrency)

	for _, t := range due {
		t := t
		g.Go(func() error {
			p.pollOne(gctx, t)
			return nil
		})
	}
	return g.Wait()
}

func (p *Poller) dueTargets(targets []adapters.Target, rt map[string]store.ServiceRuntime, now time.Time) []adapters.Target {
	var due []adapters.Target
	for _, t := range targets {
		state, ok := rt[t.Slug]
		if !ok {
			continue
		}
		interval := time.Duration(t.PollIntervalSeconds) * time.Second
		if !state.Status.Healthy() {
			interval = time.Duration(t.ActiveIntervalSeconds) * time.Second
		}
		if state.LastPolledAt == nil || now.Sub(*state.LastPolledAt) >= interval {
			due = append(due, t)
		}
	}
	return due
}

func (p *Poller) pollOne(ctx context.Context, t adapters.Target) {
	ids, _ := p.snapshot()
	serviceID, ok := ids[t.Slug]
	if !ok {
		return
	}

	adapter, ok := adapters.Get(t.Kind)
	if !ok {
		p.log.Error("adapter not registered", "vendor", t.Slug, "kind", t.Kind)
		return
	}

	start := time.Now()
	rep, err := adapter.Fetch(ctx, t)
	elapsed := time.Since(start)

	if err != nil {
		if ctx.Err() != nil {
			return
		}
		p.log.Warn("poll failed", "vendor", t.Slug, "duration", elapsed.Round(time.Millisecond), "error", err)
		if recErr := p.store.RecordFailure(ctx, serviceID, rep.HTTPStatus, err.Error(), int(elapsed.Milliseconds())); recErr != nil {
			p.log.Error("failed to record poll failure", "vendor", t.Slug, "error", recErr)
		}
		return
	}

	changed, err := p.store.ApplyReport(ctx, serviceID, rep)
	if err != nil {
		p.log.Error("failed to store poll result", "vendor", t.Slug, "error", err)
		if recErr := p.store.RecordFailure(ctx, serviceID, rep.HTTPStatus, err.Error(), int(elapsed.Milliseconds())); recErr != nil {
			p.log.Error("failed to record the rejected poll", "vendor", t.Slug, "error", recErr)
		}
		return
	}

	if changed {
		p.log.Info("status changed",
			"vendor", t.Slug,
			"status", rep.Global,
			"components", len(rep.Components),
			"duration", elapsed.Round(time.Millisecond))
	}
}
