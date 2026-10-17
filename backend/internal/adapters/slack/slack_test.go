package slack

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

func serveSlack(t *testing.T, current, history string) (*httptest.Server, *Slack) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2.0.0/current":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(current))
		case "/api/v2.0.0/history":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(history))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, NewSlack(srv.Client())
}

func TestSlackFetchRealFixture(t *testing.T) {
	current, err := os.ReadFile(filepath.Join("testdata", "slack_current.json"))
	if err != nil {
		t.Fatalf("read current fixture: %v", err)
	}
	history, err := os.ReadFile(filepath.Join("testdata", "slack_history.json"))
	if err != nil {
		t.Fatalf("read history fixture: %v", err)
	}

	srv, adapter := serveSlack(t, string(current), string(history))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "slack", URL: srv.URL, Kind: KindSlack,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(rep.Components))
	}
	if rep.Components[0].Name != "All services" || rep.Components[0].Status != adapters.HealthOK {
		t.Errorf("component = %+v", rep.Components[0])
	}
	if rep.SourceUpdatedAt.IsZero() {
		t.Error("SourceUpdatedAt is zero")
	}

	if len(rep.Incidents) != 3 {
		t.Fatalf("incidents = %d, want 3", len(rep.Incidents))
	}
	first := rep.Incidents[0]
	if first.Name != "Single Sign On errors" {
		t.Errorf("incident title = %q", first.Name)
	}
	if first.SourceID != "1581" {
		t.Errorf("incident id = %q, want 1581", first.SourceID)
	}
	if first.Impact != "Login/SSO" {
		t.Errorf("impact = %q, want Login/SSO", first.Impact)
	}
	if first.StartedAt.IsZero() {
		t.Error("incident start is zero")
	}
	if first.ResolvedAt == nil {
		t.Error("resolved incident should have a resolution time")
	}
	if !strings.HasPrefix(first.URL, "https://slack-status.com/") {
		t.Errorf("incident url = %q", first.URL)
	}
}

func TestSlackStatusVocabulary(t *testing.T) {
	cases := map[string]adapters.Health{
		"ok":          adapters.HealthOK,
		"notice":      adapters.HealthDegraded,
		"incident":    adapters.HealthPartial,
		"outage":      adapters.HealthMajor,
		"maintenance": adapters.HealthMaintenance,
		"something":   adapters.HealthUnknown,
	}

	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			srv, adapter := serveSlack(t,
				`{"status":"`+raw+`","date_updated":"2026-10-13T17:28:54-07:00"}`,
				`[]`)
			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if rep.Global != want {
				t.Errorf("Global = %q, want %q", rep.Global, want)
			}
			if rep.Components[0].Status != want {
				t.Errorf("component = %q, want %q", rep.Components[0].Status, want)
			}
		})
	}
}

func TestSlackUnresolvedIncidentHasNoResolutionTime(t *testing.T) {
	srv, adapter := serveSlack(t,
		`{"status":"incident"}`,
		`[{"id":7,"title":"Ongoing","status":"identified","date_created":"2026-10-13T17:23:07-07:00","services":["Messaging","Connectivity"]}]`)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Incidents) != 1 {
		t.Fatalf("incidents = %d, want 1", len(rep.Incidents))
	}
	if rep.Incidents[0].ResolvedAt != nil {
		t.Error("unresolved incident must not have a resolution time")
	}
	if rep.Incidents[0].Impact != "Messaging, Connectivity" {
		t.Errorf("impact = %q", rep.Incidents[0].Impact)
	}
}

func TestSlackRejects(t *testing.T) {
	cases := []struct {
		name    string
		current string
		history string
		want    string
	}{
		{"no status field", `{}`, `[]`, "no status"},
		{"current not json", `<html>nope</html>`, `[]`, "decode current"},
		{"history not json", `{"status":"ok"}`, `nope`, "decode history"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveSlack(t, tc.current, tc.history)
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

func TestSlackHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	adapter := NewSlack(srv.Client())
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error on HTTP 403")
	}
	if rep.HTTPStatus != http.StatusForbidden {
		t.Errorf("HTTPStatus = %d, want 403", rep.HTTPStatus)
	}
}
