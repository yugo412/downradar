package summary

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/store"
)

type fakeStore struct {
	detail  *store.VendorDetail
	getErr  error
	saved   []savedSummary
	cleared int
	savedCh chan struct{}
}

type savedSummary struct {
	serviceID   int64
	text        string
	model       string
	inputHash   string
	generatedAt time.Time
}

func (f *fakeStore) GetVendor(context.Context, string, store.Viewer) (*store.VendorDetail, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.detail == nil {
		return nil, store.ErrNotFound
	}
	return f.detail, nil
}

func (f *fakeStore) SaveGeneratedSummary(_ context.Context, serviceID int64, text, model, inputHash string, generatedAt time.Time) error {
	f.saved = append(f.saved, savedSummary{
		serviceID: serviceID, text: text, model: model, inputHash: inputHash, generatedAt: generatedAt,
	})
	if f.detail != nil {
		f.detail.GeneratedSummary = &store.GeneratedSummary{
			Text: text, GeneratedAt: generatedAt, Model: model, InputHash: inputHash,
		}
	}
	if f.savedCh != nil {
		f.savedCh <- struct{}{}
	}
	return nil
}

func (f *fakeStore) ClearGeneratedSummary(context.Context, int64) error {
	f.cleared++
	if f.detail != nil {
		f.detail.GeneratedSummary = nil
	}
	return nil
}

type fakeWriter struct {
	calls  int
	system string
	user   string
	answer string
	err    error
}

func (w *fakeWriter) Write(_ context.Context, system, user string) (string, error) {
	w.calls++
	w.system = system
	w.user = user
	return w.answer, w.err
}

func unhealthyDetail() *store.VendorDetail {
	since := time.Date(2026, 10, 27, 1, 40, 0, 0, time.UTC)
	started := since.Add(-5 * time.Minute)
	return &store.VendorDetail{
		VendorSummary: store.VendorSummary{
			Slug:      "example",
			Name:      "Example",
			URL:       "https://status.example.com",
			Status:    string(adapters.HealthMajor),
			ChangedAt: &since,
		},
		ComponentsList: []store.ComponentView{
			{
				SourceID: "api", Name: "API", Status: string(adapters.HealthDegraded), ChangedAt: &since,
			},
			{
				SourceID: "web", Name: "Website", Status: string(adapters.HealthOK), ChangedAt: &since,
			},
		},
		Incidents: []store.IncidentView{
			{
				SourceID: "incident-1", Name: "API latency", Status: "investigating",
				Impact: "minor", StartedAt: &started,
			},
		},
		RecentEvents: []store.EventView{
			{Status: string(adapters.HealthDegraded), Previous: string(adapters.HealthOK), Component: "API", ChangedAt: since},
		},
	}
}

func healthyDetail() *store.VendorDetail {
	since := time.Date(2026, 10, 27, 1, 40, 0, 0, time.UTC)
	return &store.VendorDetail{
		VendorSummary: store.VendorSummary{
			Slug: "example", Name: "Example", URL: "https://status.example.com",
			Status: string(adapters.HealthOK), ChangedAt: &since,
		},
		ComponentsList: []store.ComponentView{
			{SourceID: "api", Name: "API", Status: string(adapters.HealthOK), ChangedAt: &since},
		},
	}
}

func testGenerator(st Store, fw *fakeWriter) *Generator {
	return &Generator{
		store:  st,
		writer: fw,
		model:  "gpt-6-luna",
		now:    func() time.Time { return time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC) },
		log:    discardLogger(),
	}
}

func TestGenerateWritesAndThenSkipsTheSameState(t *testing.T) {
	fs := &fakeStore{detail: unhealthyDetail()}
	fw := &fakeWriter{answer: "API has been degraded since 01:40 UTC while an incident is open."}
	g := testGenerator(fs, fw)

	if err := g.Generate(context.Background(), 42, "example"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if fw.calls != 1 {
		t.Fatalf("writer calls = %d, want 1", fw.calls)
	}
	if len(fs.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(fs.saved))
	}
	saved := fs.saved[0]
	if saved.serviceID != 42 || saved.text != fw.answer || saved.model != "gpt-6-luna" || saved.inputHash == "" {
		t.Errorf("saved = %+v", saved)
	}
	if !saved.generatedAt.Equal(time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("generatedAt = %v", saved.generatedAt)
	}
	for _, want := range []string{"API", "API latency", "investigating", "componentsNeedingAttention", "2026-10-27T02:00:00Z"} {
		if !strings.Contains(fw.user, want) {
			t.Errorf("prompt does not contain %q:\n%s", want, fw.user)
		}
	}
	if !strings.Contains(fw.system, "Use only the JSON state supplied") {
		t.Errorf("system prompt = %q", fw.system)
	}

	if err := g.Generate(context.Background(), 42, "example"); err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if fw.calls != 1 || len(fs.saved) != 1 {
		t.Errorf("unchanged state called the writer again: calls=%d saved=%d", fw.calls, len(fs.saved))
	}
}

func TestGenerateReplacesTheSentenceWhenTheStateMoves(t *testing.T) {
	fs := &fakeStore{detail: unhealthyDetail()}
	fw := &fakeWriter{answer: "API has been degraded since 01:40 UTC."}
	g := testGenerator(fs, fw)

	if err := g.Generate(context.Background(), 42, "example"); err != nil {
		t.Fatalf("first Generate: %v", err)
	}

	fs.detail.Status = string(adapters.HealthPartial)
	fw.answer = "The outage now covers two components."
	if err := g.Generate(context.Background(), 42, "example"); err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if fw.calls != 2 {
		t.Errorf("writer calls = %d, want 2", fw.calls)
	}
	if fs.cleared != 1 {
		t.Errorf("cleared = %d, want the old sentence removed first", fs.cleared)
	}
	if len(fs.saved) != 2 || fs.saved[1].text != fw.answer {
		t.Errorf("saved = %+v", fs.saved)
	}
}

func TestGenerateRefreshesWhenTheModelChanges(t *testing.T) {
	fs := &fakeStore{detail: unhealthyDetail()}
	fw := &fakeWriter{answer: "API has been degraded since 01:40 UTC."}
	g := testGenerator(fs, fw)

	if err := g.Generate(context.Background(), 42, "example"); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	g.model = "gpt-6-astra"
	if err := g.Generate(context.Background(), 42, "example"); err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if fw.calls != 2 {
		t.Errorf("writer calls = %d, want a fresh sentence for the new model", fw.calls)
	}
	if len(fs.saved) != 2 || fs.saved[1].model != "gpt-6-astra" {
		t.Errorf("saved = %+v", fs.saved)
	}
}

func TestGenerateClearsAHealthyVendorsSentence(t *testing.T) {
	fs := &fakeStore{detail: healthyDetail()}
	fs.detail.GeneratedSummary = &store.GeneratedSummary{
		Text: "An old sentence.", GeneratedAt: time.Now().UTC(), InputHash: "old",
	}
	fw := &fakeWriter{answer: "should not be used"}
	g := testGenerator(fs, fw)

	if err := g.Generate(context.Background(), 42, "example"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if fw.calls != 0 {
		t.Errorf("writer calls = %d, want none for a healthy vendor", fw.calls)
	}
	if fs.cleared != 1 {
		t.Errorf("cleared = %d, want 1", fs.cleared)
	}
	if fs.detail.GeneratedSummary != nil {
		t.Errorf("summary still present: %+v", fs.detail.GeneratedSummary)
	}
}

func TestGenerateDropsTheOldSentenceWhenTheWriterFails(t *testing.T) {
	fs := &fakeStore{detail: unhealthyDetail()}
	fs.detail.GeneratedSummary = &store.GeneratedSummary{
		Text: "An old sentence.", GeneratedAt: time.Now().UTC(), InputHash: "old",
	}
	fw := &fakeWriter{err: errors.New("model unavailable")}
	g := testGenerator(fs, fw)

	if err := g.Generate(context.Background(), 42, "example"); err == nil {
		t.Fatal("Generate succeeded while the writer failed")
	}
	if fs.cleared != 1 {
		t.Errorf("cleared = %d, want the stale sentence removed", fs.cleared)
	}
	if len(fs.saved) != 0 {
		t.Errorf("saved = %d, want none", len(fs.saved))
	}
}

func TestGenerateRejectsEmptyOrOversizedAnswers(t *testing.T) {
	for name, answer := range map[string]string{
		"empty":    "",
		"too long": strings.Repeat("a", maxSummaryRunes+1),
	} {
		t.Run(name, func(t *testing.T) {
			fs := &fakeStore{detail: unhealthyDetail()}
			fw := &fakeWriter{answer: answer}
			g := testGenerator(fs, fw)

			if err := g.Generate(context.Background(), 42, "example"); err == nil {
				t.Fatal("Generate succeeded with an unusable answer")
			}
			if len(fs.saved) != 0 {
				t.Errorf("saved = %d, want none", len(fs.saved))
			}
		})
	}
}

func TestRunDrainsQueuedJobs(t *testing.T) {
	fs := &fakeStore{detail: unhealthyDetail(), savedCh: make(chan struct{}, 1)}
	fw := &fakeWriter{answer: "API has been degraded since 01:40 UTC."}
	g := testGenerator(fs, fw)
	g.jobs = make(chan job, 1)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		g.Run(ctx)
		close(done)
	}()

	g.Enqueue(42, "example")
	select {
	case <-fs.savedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker did not save the queued summary")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker did not stop when the context ended")
	}
	if len(fs.saved) != 1 || fs.saved[0].text != fw.answer {
		t.Errorf("saved = %+v", fs.saved)
	}
}

func TestCleanSummaryCollapsesLinesAndQuotes(t *testing.T) {
	got := cleanSummary("  \"Summary: The API is down.\n\nIt began at 01:40.\"  ")
	want := "The API is down. It began at 01:40."
	if got != want {
		t.Errorf("cleanSummary = %q, want %q", got, want)
	}
}
