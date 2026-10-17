package sorryapp

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

func sorryappFixture(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", "sorryapp_"+name+".json"))
	if err != nil {
		t.Fatalf("read %s fixture: %v", name, err)
	}
	return string(body)
}

func serveSorryapp(t *testing.T, status, components, notices string) (*httptest.Server, *Sorryapp) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case "/api/v1/status":
			body = status
		case "/api/v1/components":
			body = components
		case "/api/v1/notices":
			body = notices
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, NewSorryapp(srv.Client())
}

func TestSorryappFetchRealFixture(t *testing.T) {
	srv, adapter := serveSorryapp(t,
		sorryappFixture(t, "status"),
		sorryappFixture(t, "components"),
		sorryappFixture(t, "notices"),
	)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "pingdom", URL: srv.URL, Kind: KindSorryapp,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
	if rep.PageID != "2273" {
		t.Errorf("PageID = %q, want 2273: the page id is the identity to pin", rep.PageID)
	}
	if rep.SourceUpdatedAt.IsZero() {
		t.Error("SourceUpdatedAt is zero")
	}

	if len(rep.Components) != 14 {
		t.Fatalf("components = %d, want 14", len(rep.Components))
	}

	// The first component is a parent, so it carries no group. Its children do.
	parent := rep.Components[0]
	if parent.Name != "Monitoring" || parent.Group != "" || parent.Position != 1 {
		t.Errorf("first component = %+v", parent)
	}
	child := rep.Components[1]
	if child.Name != "Uptime Monitoring" || child.Group != "Monitoring" {
		t.Errorf("second component = %+v, want it grouped under Monitoring", child)
	}

	if len(rep.Incidents) != 13 {
		t.Fatalf("incidents = %d, want 13 notices", len(rep.Incidents))
	}
	first := rep.Incidents[0]
	if first.Name != "Performance issues in my Pingdom" {
		t.Errorf("notice subject = %q", first.Name)
	}
	if first.Status != "resolved" || first.Impact != "unplanned" {
		t.Errorf("notice state/type = %q/%q", first.Status, first.Impact)
	}
	if !strings.HasPrefix(first.URL, "https://status.pingdom.com/notices/") {
		t.Errorf("notice url = %q", first.URL)
	}
	if first.StartedAt.IsZero() || first.ResolvedAt == nil {
		t.Errorf("notice times = %v / %v, want both", first.StartedAt, first.ResolvedAt)
	}
}

func TestSorryappStateVocabulary(t *testing.T) {
	cases := map[string]adapters.Health{
		"operational": adapters.HealthOK,
		"degraded":    adapters.HealthDegraded,
		"Operational": adapters.HealthOK,
		"something":   adapters.HealthUnknown,
	}

	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			srv, adapter := serveSorryapp(t,
				`{"page":{"id":1,"state":"`+raw+`"}}`,
				`{"components":[{"id":1,"name":"API","state":"`+raw+`","position":1}]}`,
				`{"notices":[]}`)

			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if rep.Global != want {
				t.Errorf("Global = %q, want %q", rep.Global, want)
			}
			if len(rep.Components) != 1 || rep.Components[0].Status != want {
				t.Errorf("component = %+v, want %q", rep.Components, want)
			}
		})
	}
}

func TestSorryappComponentGrouping(t *testing.T) {
	srv, adapter := serveSorryapp(t,
		`{"page":{"id":9,"state":"degraded"}}`,
		`{"components":[
			{"id":1,"name":"Parent","state":"degraded","parent_id":null,"position":1},
			{"id":2,"name":"Child","state":"operational","parent_id":1,"position":2},
			{"id":3,"name":"Standalone","state":"operational","parent_id":null,"position":3}
		]}`,
		`{"notices":[]}`)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 3 {
		t.Fatalf("components = %d, want 3", len(rep.Components))
	}

	if rep.Components[0].Group != "" {
		t.Errorf("a top level component should carry no group: %+v", rep.Components[0])
	}
	if rep.Components[1].Group != "Parent" {
		t.Errorf("child group = %q, want the parent's name", rep.Components[1].Group)
	}
	if rep.Components[2].Group != "" {
		t.Errorf("a standalone component should carry no group: %+v", rep.Components[2])
	}
	if rep.Components[0].SourceID != "1" || rep.Components[2].Position != 3 {
		t.Errorf("id/position came out wrong: %+v %+v", rep.Components[0], rep.Components[2])
	}
}

func TestSorryappNotices(t *testing.T) {
	srv, adapter := serveSorryapp(t,
		`{"page":{"id":9,"state":"degraded"}}`,
		`{"components":[]}`,
		`{"notices":[
			{"id":1,"type":"unplanned","state":"investigating","subject":"Ongoing","url":"https://x/1","began_at":"2026-10-01T00:00:00.000Z","ended_at":null},
			{"id":2,"type":"planned","state":"scheduled","subject":"Upgrade","url":"https://x/2","began_at":"2026-10-05T00:00:00.000Z","ended_at":"2026-10-05T02:00:00.000Z"}
		]}`)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Incidents) != 2 {
		t.Fatalf("incidents = %d, want 2", len(rep.Incidents))
	}

	ongoing := rep.Incidents[0]
	if ongoing.ResolvedAt != nil {
		t.Error("a notice with no ended_at must not get a resolution time")
	}
	if ongoing.Impact != "unplanned" || ongoing.Status != "investigating" {
		t.Errorf("ongoing notice = %+v", ongoing)
	}

	planned := rep.Incidents[1]
	if planned.Impact != "planned" || planned.ResolvedAt == nil {
		t.Errorf("planned maintenance = %+v, want the type kept and a resolution time", planned)
	}
}

func TestSorryappIncludeFilter(t *testing.T) {
	srv, adapter := serveSorryapp(t,
		`{"page":{"id":9,"state":"operational"}}`,
		`{"components":[
			{"id":1,"name":"API","state":"operational","position":1},
			{"id":2,"name":"Dashboard","state":"degraded","position":2}
		]}`,
		`{"notices":[]}`)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Include: []string{"  dashboard  "},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 || rep.Components[0].Name != "Dashboard" {
		t.Errorf("components = %+v", rep.Components)
	}
}

func TestSorryappRejects(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   string
	}{
		{"no page state", `{"page":{"id":1}}`, "no page state"},
		{"status not json", `<html>nope</html>`, "decode status"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveSorryapp(t, tc.status, `{"components":[]}`, `{"notices":[]}`)
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

func TestSorryappHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	adapter := NewSorryapp(srv.Client())
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error on HTTP 403")
	}
	if rep.HTTPStatus != http.StatusForbidden {
		t.Errorf("HTTPStatus = %d, want 403", rep.HTTPStatus)
	}
}
