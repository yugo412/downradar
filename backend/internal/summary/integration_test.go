package summary

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/store"
)

func integrationStore(t *testing.T) *store.Store {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set, skipping the integration test")
	}

	ctx := context.Background()
	st, err := store.New(ctx, dsn, store.Options{AppKey: "test-app-key"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func TestGenerateStoresAndThenClearsTheSentence(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	name := fmt.Sprintf("Zz Summary Vendor %d", time.Now().UnixNano())
	slug, err := st.CreateService(ctx, store.NewService{
		Name: name,
		URL:  "https://status.example.com",
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { _ = st.PurgeService(context.Background(), slug) })

	serviceID, _, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	now := time.Date(2026, 10, 27, 2, 0, 0, 0, time.UTC)
	started := now.Add(-20 * time.Minute)
	rep := adapters.Report{
		Global:    adapters.HealthMajor,
		FetchedAt: now,
		Components: []adapters.ComponentStatus{
			{SourceID: "api", Name: "API", Position: 1, Status: adapters.HealthDegraded},
		},
		Incidents: []adapters.Incident{
			{
				SourceID: "incident-1", Name: "API latency", Status: "investigating",
				Impact: "minor", StartedAt: started,
			},
		},
	}
	if _, err := st.ApplyReport(ctx, serviceID, rep); err != nil {
		t.Fatalf("ApplyReport: %v", err)
	}

	fw := &fakeWriter{answer: "The API has been degraded since 01:40 UTC while an incident is open."}
	g := testGenerator(st, fw)
	if err := g.Generate(ctx, serviceID, slug); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	detail, err := st.GetVendor(ctx, slug, store.Public())
	if err != nil {
		t.Fatalf("GetVendor: %v", err)
	}
	if detail.GeneratedSummary == nil {
		t.Fatal("the generated summary was not stored")
	}
	if detail.GeneratedSummary.Text != fw.answer {
		t.Errorf("summary = %q, want %q", detail.GeneratedSummary.Text, fw.answer)
	}
	if detail.GeneratedSummary.Model != "gpt-6-luna" {
		t.Errorf("model = %q", detail.GeneratedSummary.Model)
	}
	if detail.GeneratedSummary.InputHash == "" {
		t.Error("stored summary has no input hash")
	}
	if !detail.GeneratedSummary.GeneratedAt.Equal(now) {
		t.Errorf("generatedAt = %v, want %v", detail.GeneratedSummary.GeneratedAt, now)
	}

	resolved := now.Add(5 * time.Minute)
	rep.Global = adapters.HealthOK
	rep.FetchedAt = resolved
	rep.Components[0].Status = adapters.HealthOK
	rep.Incidents[0].ResolvedAt = &resolved
	if _, err := st.ApplyReport(ctx, serviceID, rep); err != nil {
		t.Fatalf("healthy ApplyReport: %v", err)
	}

	// The worker has not cleared the row yet, but the page must already stop
	// showing the old sentence above a healthy vendor.
	detail, err = st.GetVendor(ctx, slug, store.Public())
	if err != nil {
		t.Fatalf("GetVendor during recovery: %v", err)
	}
	if detail.GeneratedSummary != nil {
		t.Errorf("healthy vendor exposed a stale summary: %+v", detail.GeneratedSummary)
	}

	if err := g.Generate(ctx, serviceID, slug); err != nil {
		t.Fatalf("healthy Generate: %v", err)
	}

	detail, err = st.GetVendor(ctx, slug, store.Public())
	if err != nil {
		t.Fatalf("GetVendor after recovery: %v", err)
	}
	if detail.GeneratedSummary != nil {
		t.Errorf("healthy vendor still has a summary: %+v", detail.GeneratedSummary)
	}
}
