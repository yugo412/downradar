package railway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"downradar.info/backend/internal/adapters"
)

const testStatus = `{
  "entries": [
    {"type": "group", "data": {"id": "g1", "name": "Railway", "components": [
      {"id": "c1", "name": "Dashboard", "uptimePercentage": "100.00%"},
      {"id": "c2", "name": "API", "uptimePercentage": "99.90%"}
    ]}},
    {"type": "group", "data": {"id": "g2", "name": "Observability", "components": [
      {"id": "c3", "name": "Logs", "uptimePercentage": "100.00%"}
    ]}}
  ],
  "activeIncidents": [
    {"id": "i1", "title": "API errors", "status": "INVESTIGATING",
     "createdAt": "2026-10-21T02:49:46Z", "resolvedAt": null,
     "components": [{"id": "c2", "name": "API", "groupName": "Railway", "impact": "PARTIAL_OUTAGE"}]}
  ],
  "recentIncidents": [
    {"id": "i1", "title": "API errors", "status": "INVESTIGATING",
     "createdAt": "2026-10-21T02:49:46Z", "resolvedAt": null,
     "components": [{"id": "c2", "name": "API", "groupName": "Railway", "impact": "PARTIAL_OUTAGE"}]},
    {"id": "i0", "title": "Builds paused", "status": "RESOLVED",
     "createdAt": "2026-10-20T10:00:00Z", "resolvedAt": "2026-10-20T10:10:00Z",
     "components": [{"id": "c3", "name": "Logs", "groupName": "Observability", "impact": "DEGRADED_PERFORMANCE"}]}
  ],
  "maintenances": [],
  "generatedAt": "2026-10-21T03:00:00Z"
}`

func TestRailwayFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, testStatus)
	}))
	defer srv.Close()

	rep, err := NewRailway(srv.Client()).Fetch(context.Background(), adapters.Target{
		Slug: "railway", URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthPartial {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthPartial)
	}
	if len(rep.Components) != 3 {
		t.Fatalf("components = %d, want 3", len(rep.Components))
	}
	cases := map[string]adapters.Health{
		"Dashboard": adapters.HealthOK,
		"API":       adapters.HealthPartial,
		"Logs":      adapters.HealthOK,
	}
	for _, c := range rep.Components {
		if want, ok := cases[c.Name]; ok && c.Status != want {
			t.Errorf("%s: status = %q, want %q", c.Name, c.Status, want)
		}
	}
	if rep.Components[1].Group != "Railway" {
		t.Errorf("group = %q, want Railway", rep.Components[1].Group)
	}
	if len(rep.Incidents) != 2 {
		t.Fatalf("incidents = %d, want 2 (the active one is not listed twice)", len(rep.Incidents))
	}
	if rep.Incidents[0].ResolvedAt != nil {
		t.Error("the active incident should be unresolved")
	}
	if rep.Incidents[1].ResolvedAt == nil {
		t.Error("the resolved incident should carry its end")
	}
}

func TestRailwayRefusesAnotherPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>not here</body></html>`)
	}))
	defer srv.Close()

	_, err := NewRailway(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a page with no components")
	}
}
