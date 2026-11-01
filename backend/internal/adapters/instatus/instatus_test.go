package instatus

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

func serve(t *testing.T, summary, components string) (*httptest.Server, *Instatus) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/summary.json":
			_, _ = w.Write([]byte(summary))
		case "/v3/components.json":
			_, _ = w.Write([]byte(components))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, NewInstatus(srv.Client())
}

func fixture(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

func TestInstatusFetchRealFixture(t *testing.T) {
	srv, adapter := serve(t,
		fixture(t, "instatus_summary.json"),
		fixture(t, "instatus_components.json"),
	)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "zed", URL: srv.URL, Kind: KindInstatus,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want ok: the page itself is up", rep.Global)
	}

	if len(rep.Components) != 5 {
		t.Fatalf("components = %d, want five rows below the two group headings", len(rep.Components))
	}

	byName := make(map[string]adapters.ComponentStatus, len(rep.Components))
	for _, c := range rep.Components {
		byName[c.Name] = c
	}
	cases := []struct {
		name   string
		group  string
		status adapters.Health
	}{
		{"Agent", "", adapters.HealthOK},
		{"Extension Registry", "Zed", adapters.HealthOK},
		{"Edit Prediction", "Zed", adapters.HealthDegraded},
		{"Collaboration", "Zed", adapters.HealthMaintenance},
		{"Delta Cloud Services", "Delta", adapters.HealthOK},
	}
	for _, tc := range cases {
		c, ok := byName[tc.name]
		if !ok {
			t.Errorf("component %q is missing", tc.name)
			continue
		}
		if c.Group != tc.group || c.Status != tc.status {
			t.Errorf("%s = group %q status %q, want group %q status %q",
				tc.name, c.Group, c.Status, tc.group, tc.status)
		}
		if c.SourceID == "" || c.Position == 0 {
			t.Errorf("%s = %+v, want a source id and a position", tc.name, c)
		}
	}

	if _, ok := byName["Zed"]; ok {
		t.Error("the parent was reported as a component, want it only as the group")
	}

	// The page orders a group by the component's own order, not by the order
	// the API happens to list the children in.
	wantOrder := []string{"Agent", "Collaboration", "Edit Prediction", "Extension Registry", "Delta Cloud Services"}
	for i, name := range wantOrder {
		if rep.Components[i].Name != name {
			t.Errorf("component %d = %q, want %q", i, rep.Components[i].Name, name)
		}
	}

	if len(rep.Incidents) != 3 {
		t.Fatalf("incidents = %d, want the incident, the maintenance and the resolved one", len(rep.Incidents))
	}

	open := rep.Incidents[0]
	if open.SourceID != "cl4a8n3070721qtmzvts760c4" || open.Name != "We're facing an issue with our API" {
		t.Errorf("open incident = %+v", open)
	}
	if open.Status != "investigating" || open.Impact != "major" || open.ResolvedAt != nil {
		t.Errorf("open incident status/impact = %q/%q resolved = %v, want investigating/major and unresolved",
			open.Status, open.Impact, open.ResolvedAt)
	}
	if want := time.Date(2026, 10, 31, 18, 55, 50, 0, time.UTC); !open.StartedAt.Equal(want) {
		t.Errorf("open incident started = %s, want %s", open.StartedAt, want)
	}
	if !strings.HasSuffix(open.URL, "/cl4a8n3070721qtmzvts760c4") {
		t.Errorf("open incident url = %q, want it resolved against the page", open.URL)
	}

	maintenance := rep.Incidents[1]
	if maintenance.Status != "scheduled" {
		t.Errorf("maintenance status = %q, want scheduled", maintenance.Status)
	}
	if want := time.Date(2026, 11, 2, 13, 0, 0, 0, time.UTC); !maintenance.StartedAt.Equal(want) {
		t.Errorf("maintenance started = %s, want %s", maintenance.StartedAt, want)
	}

	resolved := rep.Incidents[2]
	if resolved.Status != "resolved" || resolved.Impact != "degraded" {
		t.Errorf("resolved incident = %+v", resolved)
	}
	if resolved.ResolvedAt == nil {
		t.Fatal("resolved incident has no end")
	}
	if want := time.Date(2026, 10, 29, 9, 58, 0, 0, time.UTC); !resolved.ResolvedAt.Equal(want) {
		t.Errorf("resolved incident ended = %s, want %s", resolved.ResolvedAt, want)
	}
}

func TestInstatusPageDown(t *testing.T) {
	srv, adapter := serve(t,
		`{"page":{"name":"Thing","url":"https://status.example.com","status":"DOWN"}}`,
		`{"components":[{"id":"one","name":"Thing","status":"OPERATIONAL","order":1,"isParent":false,"children":[]}]}`,
	)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want major: the page reports down", rep.Global)
	}
}

func TestInstatusFallsBackToTheWorstComponent(t *testing.T) {
	srv, adapter := serve(t,
		`{"page":{"name":"Thing","url":"https://status.example.com","status":"SOMETHINGNEW"}}`,
		`{"components":[
			{"id":"one","name":"One","status":"DEGRADEDPERFORMANCE","order":1,"isParent":false,"children":[]},
			{"id":"two","name":"Two","status":"PARTIALOUTAGE","order":2,"isParent":false,"children":[]}
		]}`,
	)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthPartial {
		t.Errorf("Global = %q, want the worst component", rep.Global)
	}
}

func TestInstatusIncludeFilter(t *testing.T) {
	srv, adapter := serve(t,
		fixture(t, "instatus_summary.json"),
		fixture(t, "instatus_components.json"),
	)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Include: []string{"  edit prediction  "},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want only the one asked for", len(rep.Components))
	}
	if rep.Components[0].Name != "Edit Prediction" {
		t.Errorf("component = %+v", rep.Components[0])
	}
}

func TestInstatusRefusesANonInstatusPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := NewInstatus(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a page with no summary")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want it to name the status", err.Error())
	}
}

func TestInstatusRefusesAPageWithoutAStatus(t *testing.T) {
	srv, adapter := serve(t, `{"page":{"name":"Thing"}}`, `{"components":[]}`)

	_, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a summary with no page status")
	}
	if !strings.Contains(err.Error(), "no page status") {
		t.Errorf("error = %q, want it to say the status is missing", err.Error())
	}
}
