package paypal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"downradar.info/backend/internal/adapters"
)

const testComponents = `{
  "result": [
    {"id": 3, "name": "Online Checkout", "displayName": "Online Checkout", "parentName": "",
     "sortOrder": 1, "category": {"name": "product"}, "status": {"production": "Operational", "sandbox": "Operational"}},
    {"id": 7, "name": "REST APIs", "displayName": "REST APIs", "parentName": "",
     "sortOrder": 2, "category": {"name": "api"}, "status": {"production": "Service Outage", "sandbox": "Operational"}},
    {"id": 9, "name": "PayPal.me", "displayName": "PayPal.me", "parentName": "Core",
     "sortOrder": 3, "category": {"name": "product"}, "status": {"production": "Under Maintenance", "sandbox": "Operational"}}
  ]
}`

const testEvents = `{
  "result": [
    {"id": 110012, "summary": "Initial Notification: Maintenance (PP-LIVE-110012)", "type": "Maintenance",
     "state": "closed", "severity": null, "pubDate": "2026-10-16T08:00:01Z",
     "startDate": "2026-10-16T05:00:00Z", "endDate": "2026-10-16T08:00:00Z"}
  ]
}`

func TestPaypalFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/components":
			_, _ = fmt.Fprint(w, testComponents)
		case "/api/v1/events":
			_, _ = fmt.Fprint(w, testEvents)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("empty User-Agent")
		}
	}))
	defer srv.Close()

	rep, err := NewPaypal(srv.Client()).Fetch(context.Background(), adapters.Target{
		Slug: "paypal", URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthMajor)
	}
	if len(rep.Components) != 3 {
		t.Fatalf("components = %d, want 3 (a sandbox-only row is not production)", len(rep.Components))
	}
	cases := map[string]adapters.Health{
		"Online Checkout": adapters.HealthOK,
		"REST APIs":       adapters.HealthMajor,
		"PayPal.me":       adapters.HealthMaintenance,
	}
	for _, c := range rep.Components {
		if want, ok := cases[c.Name]; ok && c.Status != want {
			t.Errorf("%s: status = %q, want %q", c.Name, c.Status, want)
		}
	}
	if len(rep.Incidents) != 1 {
		t.Fatalf("incidents = %d, want 1", len(rep.Incidents))
	}
	if rep.Incidents[0].ResolvedAt == nil {
		t.Error("a closed event should be resolved")
	}
}

func TestPaypalRefusesAnotherPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>not here</body></html>`)
	}))
	defer srv.Close()

	_, err := NewPaypal(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a page with no components")
	}
}
