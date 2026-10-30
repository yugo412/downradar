// Package summary writes and stores the short sentence that sits above a
// vendor's open incidents. It reads the structured state the site already keeps,
// so the page never has to call a model while a visitor is waiting for it.
package summary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"downradar.info/backend/internal/store"
)

// Store is the narrow part of the database the writer needs. Keeping it here
// makes the generator testable without a model or a live database.
type Store interface {
	GetVendor(context.Context, string, store.Viewer) (*store.VendorDetail, error)
	SaveGeneratedSummary(context.Context, int64, string, string, string, time.Time) error
	ClearGeneratedSummary(context.Context, int64) error
}

// Writer turns the system and user prompts into plain text. OpenAI is the real
// one; tests pass a fake so they never call the network.
type Writer interface {
	Write(context.Context, string, string) (string, error)
}

type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	Timeout time.Duration
}

const queueSize = 256

type job struct {
	serviceID int64
	slug      string
}

type Generator struct {
	store  Store
	writer Writer
	model  string
	now    func() time.Time
	log    *slog.Logger
	jobs   chan job
}

func New(st Store, cfg Config, log *slog.Logger) *Generator {
	model := strings.TrimSpace(cfg.Model)
	return &Generator{
		store:  st,
		writer: NewOpenAI(cfg.APIKey, cfg.BaseURL, model, cfg.Timeout),
		model:  model,
		now:    time.Now,
		log:    log,
		jobs:   make(chan job, queueSize),
	}
}

// Enqueue asks the worker to refresh one vendor after its poll. It never waits:
// a full queue means the next poll will ask again, and a model call must not slow
// down the status collection itself.
func (g *Generator) Enqueue(serviceID int64, slug string) {
	if g.jobs == nil {
		return
	}
	select {
	case g.jobs <- job{serviceID: serviceID, slug: slug}:
	default:
		if g.log != nil {
			g.log.Warn("summary queue is full, leaving the sentence for the next poll", "vendor", slug)
		}
	}
}

// Run drains queued vendors until the process stops. One worker is enough: the
// network is only called when the stored state changed, and the queue keeps a
// slow model away from the poller.
func (g *Generator) Run(ctx context.Context) {
	if g.jobs == nil {
		return
	}
	if g.log != nil {
		g.log.Info("summary worker started", "queue", cap(g.jobs))
	}
	for {
		select {
		case <-ctx.Done():
			if g.log != nil {
				g.log.Info("summary worker stopped")
			}
			return
		case next := <-g.jobs:
			if err := g.Generate(ctx, next.serviceID, next.slug); err != nil && ctx.Err() == nil && g.log != nil {
				g.log.Warn("generated summary was not refreshed", "vendor", next.slug, "error", err)
			}
		}
	}
}

// Generate refreshes one vendor's summary if its state differs from the state the
// stored sentence describes. The fingerprint is checked on every call, so the
// poller can ask after each successful poll and still write only when something
// changed. A healthy vendor has its old sentence removed.
func (g *Generator) Generate(ctx context.Context, serviceID int64, slug string) error {
	detail, err := g.store.GetVendor(ctx, slug, store.Viewer{IsStaff: true})
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("summary: read %s: %w", slug, err)
	}

	state := buildState(detail)
	if !state.worthSummarizing() {
		if detail.GeneratedSummary != nil {
			return g.store.ClearGeneratedSummary(ctx, serviceID)
		}
		return nil
	}

	hash := state.fingerprint()
	if hash == "" {
		return errors.New("summary: empty fingerprint")
	}
	if detail.GeneratedSummary != nil &&
		detail.GeneratedSummary.InputHash == hash &&
		detail.GeneratedSummary.Model == g.model &&
		strings.TrimSpace(detail.GeneratedSummary.Text) != "" {
		return nil
	}

	// The old sentence was written for a different state. Removing it first is
	// deliberate: if the model fails, the page shows no summary rather than one
	// that describes a state the vendor has already left.
	if detail.GeneratedSummary != nil {
		if err := g.store.ClearGeneratedSummary(ctx, serviceID); err != nil {
			return err
		}
	}

	now := g.now().UTC()
	system, user := state.prompt(now)
	text, err := g.writer.Write(ctx, system, user)
	if err != nil {
		return fmt.Errorf("summary: write %s: %w", slug, err)
	}
	text = cleanSummary(text)
	if text == "" {
		return fmt.Errorf("summary: model returned no text for %s", slug)
	}
	if size := utf8.RuneCountInString(text); size > maxSummaryRunes {
		return fmt.Errorf("summary: model returned %d characters for %s, limit is %d", size, slug, maxSummaryRunes)
	}

	if err := g.store.SaveGeneratedSummary(ctx, serviceID, text, g.model, hash, now); err != nil {
		return err
	}
	if g.log != nil {
		g.log.Info("vendor summary generated", "vendor", slug, "model", g.model)
	}
	return nil
}

// cleanSummary makes a model's answer safe to put in a single paragraph: no
// surrounding quotes, no line breaks that would turn it into a list.
func cleanSummary(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, `"'`)
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "Summary:")
	text = strings.TrimSpace(text)
	text = strings.Trim(text, `"'`)
	return strings.Join(strings.Fields(text), " ")
}
