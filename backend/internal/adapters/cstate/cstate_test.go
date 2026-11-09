package cstate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

func fixture(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

func serve(t *testing.T, body string) (*httptest.Server, *CState) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, NewCState(srv.Client())
}

func TestCStateFetchRealFixture(t *testing.T) {
	srv, adapter := serve(t, fixture(t, "cstate_index.json"))

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "flathub", URL: srv.URL, Kind: KindCState,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
	if len(rep.Components) != 5 {
		t.Fatalf("components = %d, want the five systems the page lists", len(rep.Components))
	}
	want := []struct {
		name  string
		group string
	}{
		{"Website", "main"},
		{"API", "main"},
		{"CDN", "main"},
		{"Build service", "main"},
		{"Main repository server", "main"},
	}
	for i, tc := range want {
		c := rep.Components[i]
		if c.Name != tc.name || c.Group != tc.group || c.Status != adapters.HealthOK {
			t.Errorf("component %d = %+v, want %q in %q, ok", i, c, tc.name, tc.group)
		}
		if c.SourceID != tc.name || c.Position != i+1 {
			t.Errorf("component %d = %+v, want the name as its id and a position", i, c)
		}
	}
	if len(rep.Incidents) != 0 {
		t.Errorf("incidents = %+v, want none on a page with no open issue", rep.Incidents)
	}
}

func TestCStateFetchOpenIssues(t *testing.T) {
	srv, adapter := serve(t, fixture(t, "cstate_incidents.json"))

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "example", URL: srv.URL, Kind: KindCState,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want major for a page whose overall state is down", rep.Global)
	}

	if len(rep.Components) != 3 {
		t.Fatalf("components = %+v, want three", rep.Components)
	}
	cases := []struct {
		name   string
		group  string
		status adapters.Health
	}{
		{"API", "North Coast", adapters.HealthMajor},
		{"Website", "North Coast", adapters.HealthPartial},
		{"Docs", "Uncategorized", adapters.HealthMaintenance},
	}
	for i, tc := range cases {
		c := rep.Components[i]
		if c.Name != tc.name || c.Group != tc.group || c.Status != tc.status {
			t.Errorf("component %d = %+v, want %q in %q, %q", i, c, tc.name, tc.group, tc.status)
		}
	}

	// The outage is listed under both systems it affects, but it is one event.
	if len(rep.Incidents) != 3 {
		t.Fatalf("incidents = %+v, want three after the duplicate is folded", rep.Incidents)
	}

	outage := rep.Incidents[0]
	if outage.SourceID != "2026-11-08-api-outage.md" || outage.Name != "API outage" {
		t.Errorf("outage = %+v", outage)
	}
	if outage.Status != "investigating" || outage.Impact != "major" || outage.ResolvedAt != nil {
		t.Errorf("outage status/impact = %q/%q resolved = %v", outage.Status, outage.Impact, outage.ResolvedAt)
	}
	if want := time.Date(2026, 11, 8, 9, 15, 0, 0, time.UTC); !outage.StartedAt.Equal(want) {
		t.Errorf("outage started = %v, want %v", outage.StartedAt, want)
	}
	if want := "https://status.example.com/issues/2026-11-08-api-outage/"; outage.URL != want {
		t.Errorf("outage URL = %q, want %q", outage.URL, want)
	}

	slow := rep.Incidents[1]
	if slow.Name != "Slow builds" || slow.Impact != "partial" || slow.Status != "investigating" {
		t.Errorf("slow builds = %+v", slow)
	}
	// The generator sometimes prints the zone twice; the reading keeps the offset.
	if want := time.Date(2026, 11, 8, 7, 0, 0, 0, time.UTC); !slow.StartedAt.Equal(want) {
		t.Errorf("slow builds started = %v, want %v", slow.StartedAt, want)
	}
	if want := srv.URL + "/issues/2026-11-08-slow-builds/"; slow.URL != want {
		t.Errorf("slow builds URL = %q, want %q", slow.URL, want)
	}

	maintenance := rep.Incidents[2]
	if maintenance.Name != "Scheduled maintenance" || maintenance.Status != "notice" || maintenance.Impact != "maintenance" {
		t.Errorf("maintenance = %+v", maintenance)
	}
}

func TestCStateKeepsOnlyTheChosenComponents(t *testing.T) {
	srv, adapter := serve(t, fixture(t, "cstate_incidents.json"))

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Kind: KindCState, Include: []string{"api"},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 1 || rep.Components[0].Name != "API" {
		t.Errorf("components = %+v, want only API", rep.Components)
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want the overall state the page reports", rep.Global)
	}
}

func TestCStateFallsBackToTheWorstSystem(t *testing.T) {
	srv, adapter := serve(t, `{
		"is": "index",
		"cStateVersion": "6.0.2",
		"summaryStatus": "",
		"systems": [
			{"name": "API", "status": "ok"},
			{"name": "Website", "status": "disrupted"}
		]
	}`)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL, Kind: KindCState})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthPartial {
		t.Errorf("Global = %q, want partial from the worst system", rep.Global)
	}
}

func TestCStateRejectsPagesItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"not json", `{"hello": "world"}`},
		{"no systems", `{"is": "index", "cStateVersion": "6.0.2", "systems": []}`},
	}
	for _, tc := range cases {
		srv, adapter := serve(t, tc.body)
		if _, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL, Kind: KindCState}); err == nil {
			t.Errorf("%s: Fetch accepted a page it should not read", tc.name)
		}
	}
}
