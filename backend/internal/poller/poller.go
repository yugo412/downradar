package poller

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"golang.org/x/sync/errgroup"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/detect"
	"downradar.info/backend/internal/store"
)

// detectionRetry is how long a vendor without a profile waits before it is
// offered to the detection queue again. A new profile arrives with a deploy, not
// in minutes, so the page is left alone in between.
const detectionRetry = time.Hour

// Summarizer queues a refresh of the generated sentence beside a vendor's stored
// state. The poller asks it only after a successful poll, never while a page is
// being rendered, and never waits for the model.
type Summarizer interface {
	Enqueue(serviceID int64, slug string)
}

type Poller struct {
	store       *store.Store
	profiles    catalog.Profiles
	detector    *detect.Detector
	summarizer  Summarizer
	concurrency int
	tick        time.Duration
	log         *slog.Logger
}

// job pairs a stored vendor with the target built from its row and profile.
type job struct {
	row    store.ServiceRow
	target adapters.Target
}

func New(st *store.Store, cat catalog.Catalog, concurrency int, tick time.Duration, log *slog.Logger) *Poller {
	return &Poller{
		store:       st,
		profiles:    cat.Profiles,
		detector:    detect.New(cat),
		concurrency: concurrency,
		tick:        tick,
		log:         log,
	}
}

// WithSummarizer turns on the generated summary written after a poll. Without
// one, the page and the alerts keep their existing wording.
func (p *Poller) WithSummarizer(s Summarizer) *Poller {
	p.summarizer = s
	return p
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

// cycle reads the vendor list from the table every time, which is what replaced
// the file watcher: a form-written row is picked up next cycle. Detection runs
// first so a vendor matched here is polled with that report, not fetched twice.
func (p *Poller) cycle(ctx context.Context) error {
	if err := p.detectMissingProfile(ctx); err != nil && ctx.Err() == nil {
		p.log.Error("profile detection pass failed", "error", err)
	}

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

// detectMissingProfile offers every vendor that has no profile to the queue. A
// vendor added without one is stored either way; this is what turns it into a
// polled vendor once a profile for its platform exists.
func (p *Poller) detectMissingProfile(ctx context.Context) error {
	rows, err := p.store.PendingDetection(ctx)
	if err != nil {
		return err
	}

	due := dueDetections(rows, time.Now().UTC())
	if len(due) == 0 {
		return nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(p.concurrency)

	for _, row := range due {
		row := row
		g.Go(func() error {
			p.detectOne(gctx, row)
			return nil
		})
	}
	return g.Wait()
}

// dueDetections keeps the queue off a page that was offered recently.
func dueDetections(rows []store.DetectRow, now time.Time) []store.DetectRow {
	var due []store.DetectRow
	for _, row := range rows {
		if row.LastPolledAt == nil || now.Sub(*row.LastPolledAt) >= detectionRetry {
			due = append(due, row)
		}
	}
	return due
}

func (p *Poller) detectOne(ctx context.Context, row store.DetectRow) {
	match, err := p.detector.Detect(ctx, row.Name, row.URL)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		p.log.Info("no profile matched, the vendor waits for a later pass",
			"vendor", row.Slug, "url", row.URL, "error", err)
		if parkErr := p.store.ParkDetection(ctx, row.ID, err.Error()); parkErr != nil {
			p.log.Error("failed to park the vendor", "vendor", row.Slug, "error", parkErr)
		}
		return
	}

	profile, ok := p.profiles[match.Profile]
	if !ok {
		p.log.Error("detected a profile that is not declared", "vendor", row.Slug, "profile", match.Profile)
		return
	}

	if err := p.store.SetDetectedProfile(ctx, row.ID, match.Profile,
		profile.PollIntervalSeconds, profile.ActiveIntervalSeconds); err != nil {
		p.log.Error("failed to record the detected profile", "vendor", row.Slug, "error", err)
		return
	}

	p.log.Info("profile detected", "vendor", row.Slug, "profile", match.Profile, "url", row.URL)

	if _, err := p.store.ApplyReport(ctx, row.ID, match.Report); err != nil {
		p.log.Warn("the detected profile could not store its first report",
			"vendor", row.Slug, "profile", match.Profile, "error", err)
		return
	}
	p.refreshSummary(row.ID, row.Slug)
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
// having problems are polled more often, unless a page refused the last poll, in
// which case it is left alone until its wait is over.
func dueJobs(jobs []job, now time.Time) []job {
	var due []job
	for _, j := range jobs {
		if j.row.NextPollAt != nil && now.Before(*j.row.NextPollAt) {
			continue
		}
		if j.row.LastPolledAt == nil || now.Sub(*j.row.LastPolledAt) >= pollInterval(j.row.Slug, baseInterval(j)) {
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

		failure := store.PollFailure{
			HTTPStatus: rep.HTTPStatus,
			Error:      err.Error(),
			DurationMS: int(elapsed.Milliseconds()),
		}
		wait, refusal := refusalWait(rep.HTTPStatus, j.row.PollRefusals+1,
			baseInterval(j), rep.RetryAfter, rand.Float64())
		if refusal {
			until := time.Now().UTC().Add(wait)
			failure.NextPollAt = &until
			failure.Refusals = j.row.PollRefusals + 1
		}

		attrs := []any{
			"vendor", j.target.Slug,
			"status", rep.HTTPStatus,
			"duration", elapsed.Round(time.Millisecond),
			"error", err,
		}
		if refusal {
			attrs = append(attrs, "wait", wait.Round(time.Second), "refusals", failure.Refusals)
		}
		p.log.Warn("poll failed", attrs...)

		first, recErr := p.store.RecordFailure(ctx, j.row.ID, failure)
		if recErr != nil {
			p.log.Error("failed to record poll failure", "vendor", j.target.Slug, "error", recErr)
		} else if first {
			p.notifyPollFailure(ctx, j, failure)
		}
		return
	}

	changed, err := p.store.ApplyReport(ctx, j.row.ID, rep)
	if err != nil {
		p.log.Error("failed to store poll result", "vendor", j.target.Slug, "error", err)
		failure := store.PollFailure{
			HTTPStatus: rep.HTTPStatus,
			Error:      err.Error(),
			DurationMS: int(elapsed.Milliseconds()),
		}
		first, recErr := p.store.RecordFailure(ctx, j.row.ID, failure)
		if recErr != nil {
			p.log.Error("failed to record the rejected poll", "vendor", j.target.Slug, "error", recErr)
		} else if first {
			p.notifyPollFailure(ctx, j, failure)
		}
		return
	}

	p.refreshSummary(j.row.ID, j.target.Slug)

	if changed {
		p.log.Info("status changed",
			"vendor", j.target.Slug,
			"status", rep.Global,
			"components", len(rep.Components),
			"duration", elapsed.Round(time.Millisecond))
	}
}

// notifyPollFailure puts the first failure after a working poll in the staff
// feed. A failure to write the notice must not hide the failed poll, so this is
// logged rather than returned.
func (p *Poller) notifyPollFailure(ctx context.Context, j job, failure store.PollFailure) {
	body := "The status page could not be read."
	if failure.HTTPStatus != 0 {
		body = fmt.Sprintf("The status page answered HTTP %d and could not be read: %s.",
			failure.HTTPStatus, failure.Error)
	} else if failure.Error != "" {
		body = fmt.Sprintf("The status page could not be read: %s.", failure.Error)
	}
	if failure.NextPollAt != nil {
		body += fmt.Sprintf(" The next attempt is at %s.",
			failure.NextPollAt.UTC().Format("2006-01-02 15:04 MST"))
	}

	serviceID := j.row.ID
	if _, err := p.store.CreateNotification(ctx, store.NotificationInput{
		Audience:  store.AudienceStaff,
		Title:     fmt.Sprintf("Polling failed for %s", j.target.Name),
		Body:      body,
		ServiceID: &serviceID,
	}); err != nil {
		p.log.Error("failed to record the polling notice", "vendor", j.target.Slug, "error", err)
	}
}

// refreshSummary is best effort: the worker refreshes the sentence in the
// background, so a model that is slow or down never turns a successful poll into
// a failure or delays the next vendor.
func (p *Poller) refreshSummary(serviceID int64, slug string) {
	if p.summarizer == nil {
		return
	}
	p.summarizer.Enqueue(serviceID, slug)
}
