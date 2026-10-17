package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"downradar.info/backend/internal/adapters"
)

func serveOpencode(t *testing.T, body string) (*httptest.Server, *Opencode) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, NewOpencode(srv.Client())
}

func TestOpencodeFetchRealFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "opencode_status.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv, adapter := serveOpencode(t, string(body))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "opencode", URL: srv.URL, Kind: KindOpencode,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
	if rep.PageID != "" {
		t.Errorf("PageID = %q, want empty: this page has no identity to pin", rep.PageID)
	}
	if rep.SourceUpdatedAt.IsZero() {
		t.Error("SourceUpdatedAt is zero, so the millisecond timestamp was not read")
	}

	want := []string{"console", "logs", "inference", "go", "payg", "free"}
	if len(rep.Components) != len(want) {
		t.Fatalf("components = %d, want %d", len(rep.Components), len(want))
	}
	for i, name := range want {
		got := rep.Components[i]
		if got.SourceID != name || got.Name != name {
			t.Errorf("component %d = %q/%q, want %q for both", i, got.SourceID, got.Name, name)
		}
		if got.Position != i+1 {
			t.Errorf("component %d position = %d, want %d", i, got.Position, i+1)
		}
	}

	if len(rep.Incidents) == 0 {
		t.Fatal("no incidents parsed")
	}

	// Exactly one incident has a written note, and that note is what gives it a
	// readable title. The rest fall back to the service and the reason.
	var noted bool
	for _, inc := range rep.Incidents {
		if inc.StartedAt.IsZero() {
			t.Errorf("incident %q has no start time", inc.SourceID)
		}
		if strings.Contains(inc.Name, "Console requests failed") {
			noted = true
			if inc.ResolvedAt == nil {
				t.Error("the noted incident is resolved but carries no resolution time")
			}
			if inc.Impact != "errors" {
				t.Errorf("impact = %q, want errors", inc.Impact)
			}
		}
	}
	if !noted {
		t.Error("the incident that has a note did not take its title from it")
	}
}

func TestOpencodeStatusVocabulary(t *testing.T) {
	cases := map[string]adapters.Health{
		"operational": adapters.HealthOK,
		"degraded":    adapters.HealthDegraded,
		"outage":      adapters.HealthMajor,
		"unknown":     adapters.HealthUnknown,
		"something":   adapters.HealthUnknown,
	}

	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			srv, adapter := serveOpencode(t, `{"status":"`+raw+`"}`)
			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if rep.Global != want {
				t.Errorf("Global = %q, want %q", rep.Global, want)
			}
		})
	}
}

func TestOpencodeStalePageReadsAsUnknown(t *testing.T) {
	srv, adapter := serveOpencode(t,
		`{"status":"operational","stale":true,"components":[{"service":"console","status":"operational"}]}`)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthUnknown {
		t.Errorf("Global = %q, want unknown while the page is stale", rep.Global)
	}
	if len(rep.Components) != 1 || rep.Components[0].Status != adapters.HealthUnknown {
		t.Errorf("components = %+v, want unknown while the page is stale", rep.Components)
	}
}

func TestOpencodeIncludeFilter(t *testing.T) {
	srv, adapter := serveOpencode(t,
		`{"status":"degraded","components":[{"service":"console","status":"operational"},{"service":"payg","status":"degraded"}]}`)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Include: []string{"  PAYG  "},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want only the one asked for", len(rep.Components))
	}
	if rep.Components[0].Name != "payg" || rep.Components[0].Status != adapters.HealthDegraded {
		t.Errorf("component = %+v", rep.Components[0])
	}
}

func TestOpencodeIncidentTitlesWithoutANote(t *testing.T) {
	cases := []struct {
		name     string
		incident string
		want     string
	}{
		{
			"service and reason",
			`{"id":"i-1","event":"i-1","service":"inference","status":"degraded","reason":"latency","startedAt":1792018800000}`,
			"inference latency",
		},
		{
			"reason none falls back to the service",
			`{"id":"i-2","event":"i-2","service":"logs","status":"operational","reason":"none","startedAt":1792018800000}`,
			"logs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveOpencode(t, `{"status":"degraded","incidents":[`+tc.incident+`]}`)
			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if len(rep.Incidents) != 1 {
				t.Fatalf("incidents = %d, want 1", len(rep.Incidents))
			}
			if rep.Incidents[0].Name != tc.want {
				t.Errorf("name = %q, want %q", rep.Incidents[0].Name, tc.want)
			}
		})
	}
}

func TestOpencodeUnresolvedIncidentHasNoResolutionTime(t *testing.T) {
	srv, adapter := serveOpencode(t,
		`{"status":"degraded","incidents":[{"id":"i-3","event":"i-3","service":"go","status":"degraded","reason":"errors","startedAt":1792018800000}]}`)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Incidents[0].ResolvedAt != nil {
		t.Error("an incident with no resolvedAt must not get a resolution time")
	}
}

func TestOpencodeRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no status field", `{"components":[]}`, "no status"},
		{"not json at all", `<html>nope</html>`, "decode json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveOpencode(t, tc.body)
			_, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestOpencodeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	adapter := NewOpencode(srv.Client())
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error on HTTP 403")
	}
	if rep.HTTPStatus != http.StatusForbidden {
		t.Errorf("HTTPStatus = %d, want 403", rep.HTTPStatus)
	}
}
