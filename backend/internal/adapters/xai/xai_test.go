package xai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

func serve(t *testing.T, body string, status int) *XAI {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &XAI{client: srv.Client(), url: srv.URL}
}

func fixture(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

func TestXAIFetchRealFixture(t *testing.T) {
	adapter := serve(t, fixture(t, "xai_summary.json"), http.StatusOK)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: "https://status.x.ai/grok-com", Kind: KindXAI,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthDegraded {
		t.Errorf("Global = %q, want degraded: the page reports a minor disruption", rep.Global)
	}
	if len(rep.Components) != 25 {
		t.Fatalf("components = %d, want the whole list", len(rep.Components))
	}

	byName := make(map[string]adapters.ComponentStatus, len(rep.Components))
	for _, c := range rep.Components {
		byName[c.Name] = c
	}
	for _, name := range []string{"grok.com", "cursor.com", "Enterprise Dashboard"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("component %q is missing", name)
		}
	}
	if got := byName["grok.com"].Status; got != adapters.HealthOK {
		t.Errorf("grok.com = %q, want ok", got)
	}
	if got := byName["cursor.com"].Status; got != adapters.HealthDegraded {
		t.Errorf("cursor.com = %q, want degraded", got)
	}
	if byName["grok.com"].Position == 0 || byName["grok.com"].SourceID == "" {
		t.Errorf("grok.com = %+v, want a position and a source id", byName["grok.com"])
	}

	if len(rep.Incidents) != 1 {
		t.Fatalf("incidents = %d, want the active one", len(rep.Incidents))
	}
	incident := rep.Incidents[0]
	if incident.SourceID != "ps_838fe2a3a154acfa" {
		t.Errorf("incident source id = %q", incident.SourceID)
	}
	if incident.Name != "Service Degradation - Billing and Usage Dashboard" {
		t.Errorf("incident name = %q", incident.Name)
	}
	if incident.Status != "investigating" || incident.Impact != "degraded" {
		t.Errorf("incident status/impact = %q/%q, want investigating/degraded",
			incident.Status, incident.Impact)
	}
	if want := time.Date(2026, 11, 1, 5, 38, 43, 0, time.UTC); !incident.StartedAt.Equal(want) {
		t.Errorf("incident started = %s, want %s", incident.StartedAt, want)
	}
	if incident.ResolvedAt != nil {
		t.Errorf("incident resolved = %v, want nil", incident.ResolvedAt)
	}
	if rep.PageID != "" {
		t.Errorf("PageID = %q, want empty: the page has no identity to pin", rep.PageID)
	}
}

func TestXAIUsesThePagesOwnStates(t *testing.T) {
	adapter := serve(t, `{
		"status":{"indicator":"critical","description":"Major outage"},
		"components":[
			{"id":"one","name":"One","status":"available"},
			{"id":"two","name":"Two","status":"info"},
			{"id":"three","name":"Three","status":"disruption"},
			{"id":"four","name":"Four","status":"outage"},
			{"id":"five","name":"Five","status":"no_data"}
		],
		"incidents":[]
	}`, http.StatusOK)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: "https://status.x.ai"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want major", rep.Global)
	}

	want := map[string]adapters.Health{
		"One":   adapters.HealthOK,
		"Two":   adapters.HealthOK,
		"Three": adapters.HealthDegraded,
		"Four":  adapters.HealthMajor,
		"Five":  adapters.HealthUnknown,
	}
	for _, c := range rep.Components {
		if c.Status != want[c.Name] {
			t.Errorf("%s = %q, want %q", c.Name, c.Status, want[c.Name])
		}
	}
}

func TestXAIFallsBackToTheWorstComponent(t *testing.T) {
	adapter := serve(t, `{
		"status":{"indicator":"somethingNew"},
		"components":[
			{"id":"one","name":"One","status":"disruption"},
			{"id":"two","name":"Two","status":"outage"}
		]
	}`, http.StatusOK)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: "https://status.x.ai"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want the worst component", rep.Global)
	}
}

func TestXAIRefusesAPageWithoutAStatus(t *testing.T) {
	adapter := serve(t, `{"components":[]}`, http.StatusOK)

	_, err := adapter.Fetch(context.Background(), adapters.Target{URL: "https://status.x.ai"})
	if err == nil {
		t.Fatal("Fetch accepted a summary with no indicator")
	}
	if !strings.Contains(err.Error(), "no status indicator") {
		t.Errorf("error = %q, want it to say the indicator is missing", err.Error())
	}
}

func TestXAIRefusesACloudflareBlock(t *testing.T) {
	adapter := serve(t, `Sorry, you have been blocked`, http.StatusForbidden)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: "https://status.x.ai"})
	if err == nil {
		t.Fatal("Fetch accepted a 403")
	}
	if rep.HTTPStatus != http.StatusForbidden {
		t.Errorf("HTTPStatus = %d, want 403", rep.HTTPStatus)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, want it to name the status", err.Error())
	}
}
