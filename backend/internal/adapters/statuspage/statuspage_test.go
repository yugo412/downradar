package statuspage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"downradar.info/backend/internal/adapters"
)

func TestStatuspageFetch(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "statuspage_v2.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/summary.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("empty User-Agent")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	adapter := NewStatuspage(srv.Client())
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "strava", URL: srv.URL, Kind: KindStatuspage,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthDegraded {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthDegraded)
	}

	if len(rep.Components) != 4 {
		t.Fatalf("components = %d, want 4 (group rows excluded)", len(rep.Components))
	}

	byName := map[string]adapters.ComponentStatus{}
	for _, c := range rep.Components {
		byName[c.Name] = c
	}
	cases := map[string]adapters.Health{
		"Mobile applications": adapters.HealthOK,
		"Strava.com":          adapters.HealthDegraded,
		"API":                 adapters.HealthPartial,
		"Something New":       adapters.HealthUnknown,
	}
	for name, want := range cases {
		got, ok := byName[name]
		if !ok {
			t.Errorf("component %q missing", name)
			continue
		}
		if got.Status != want {
			t.Errorf("%s: status = %q, want %q", name, got.Status, want)
		}
	}

	if len(rep.Incidents) != 2 {
		t.Fatalf("incidents = %d, want 2", len(rep.Incidents))
	}
	if rep.Incidents[0].ResolvedAt != nil {
		t.Error("first incident should be unresolved")
	}
	if rep.Incidents[1].ResolvedAt == nil {
		t.Error("second incident should be resolved")
	}

	if rep.SourceUpdatedAt.IsZero() {
		t.Error("SourceUpdatedAt is zero")
	}
	if rep.FetchedAt.IsZero() {
		t.Error("FetchedAt is zero")
	}
}

func TestStatuspageInclude(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "statuspage_v2.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	adapter := NewStatuspage(srv.Client())
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "strava", URL: srv.URL, Kind: KindStatuspage,
		Include: []string{"api", "STRAVA.COM"},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(rep.Components))
	}
}

func TestStatuspageRejectsNonStatuspage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()

	adapter := NewStatuspage(srv.Client())
	if _, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL}); err == nil {
		t.Fatal("expected error because page.id is empty")
	}
}

func TestStatuspageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	adapter := NewStatuspage(srv.Client())
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected error on HTTP 429")
	}
	if rep.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("HTTPStatus = %d, want 429", rep.HTTPStatus)
	}
}
