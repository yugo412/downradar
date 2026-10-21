package auth0

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"downradar.info/backend/internal/adapters"
)

const testPage = `<!doctype html><html><head></head><body>
<script id="__NEXT_DATA__" type="application/json">{
  "props": {"pageProps": {
    "activeIncidents": [
      {"region": "US-1", "environment": "Production", "response": {"uptime": "99.98%", "incidents": [
        {"id": "planned-1", "name": "Planned Maintenance", "status": "scheduled", "impact": "maintenance",
         "started_at": "2026-10-19T16:31:25Z", "resolved_at": null}
      ]}},
      {"region": "EU-1", "environment": "Production", "response": {"uptime": "99.00%", "incidents": [
        {"id": "outage-1", "name": "Authentication errors", "status": "investigating", "impact": "critical",
         "started_at": "2026-10-20T08:00:00Z", "resolved_at": null}
      ]}}
    ],
    "timeline": {"US-1": {"Production": {}}, "EU-1": {"Production": {}}, "AU": {"Production": {}}}
  }}
}</script>
</body></html>`

func TestAuth0Fetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, testPage)
	}))
	defer srv.Close()

	rep, err := NewAuth0(srv.Client()).Fetch(context.Background(), adapters.Target{
		Slug: "auth0", URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthMajor)
	}
	if len(rep.Components) != 3 {
		t.Fatalf("components = %d, want 3", len(rep.Components))
	}
	cases := map[string]adapters.Health{
		"AU":   adapters.HealthOK,
		"EU-1": adapters.HealthMajor,
		"US-1": adapters.HealthOK,
	}
	for _, c := range rep.Components {
		if want, ok := cases[c.Name]; ok && c.Status != want {
			t.Errorf("%s: status = %q, want %q", c.Name, c.Status, want)
		}
	}
	if len(rep.Incidents) != 2 {
		t.Fatalf("incidents = %d, want 2", len(rep.Incidents))
	}
	if rep.Incidents[0].ResolvedAt != nil {
		t.Error("the active incident should be unresolved")
	}
}

func TestAuth0RefusesAnotherPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>some other page</body></html>`)
	}))
	defer srv.Close()

	_, err := NewAuth0(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a page with no data")
	}
}
