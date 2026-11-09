package statusiq

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

const testPage = `<!doctype html>
<script>var statuspages = {"globals":{"enc_statuspage_id":"test-page-id="}};</script>
`

func fixture(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

func serve(t *testing.T, page, summary string) (*httptest.Server, *StatusIQ) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, page)
		case strings.HasPrefix(r.URL.Path, "/sp/api/public/summary_details/statuspages/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, summary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, NewStatusIQ(srv.Client())
}

func TestStatusIQFetchRealFixture(t *testing.T) {
	srv, adapter := serve(t, testPage, fixture(t, "statusiq_canonical.json"))

	// The page is hash routed, as Canonical's is, so the adapter has to read
	// the origin without the fragment.
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "canonical", URL: srv.URL + "/#/", Kind: KindStatusIQ,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
	if rep.PageID != "test-page-id=" {
		t.Errorf("PageID = %q, want the id from the page", rep.PageID)
	}
	if len(rep.Components) != 43 {
		t.Fatalf("components = %d, want the 43 the page lists", len(rep.Components))
	}
	first := rep.Components[0]
	if first.Name != "Azure archive mirrors" || first.Status != adapters.HealthOK || first.Group != "" {
		t.Errorf("first component = %+v", first)
	}
	if first.Position != 1 || first.SourceID == "" {
		t.Errorf("first component = %+v, want a position and a source id", first)
	}
	if len(rep.Incidents) == 0 {
		t.Error("incidents are empty, want the history the page still lists")
	}
	for _, inc := range rep.Incidents {
		if inc.Status == "resolved" && inc.ResolvedAt == nil {
			t.Errorf("resolved incident %q has no resolved time", inc.Name)
		}
		if inc.ResolvedAt == nil && inc.Status != "scheduled" {
			t.Errorf("incident %q is open but the page is up", inc.Name)
		}
	}
}

func TestStatusIQFlattensGroups(t *testing.T) {
	srv, adapter := serve(t, testPage, fixture(t, "statusiq_manageengine.json"))

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL + "/", Kind: KindStatusIQ,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 19 {
		t.Fatalf("components = %d, want the 12 rows and the 7 under the three groups", len(rep.Components))
	}
	grouped := map[string]int{}
	for _, c := range rep.Components {
		if c.Group != "" {
			grouped[c.Group]++
		}
	}
	if len(grouped) != 3 || grouped["Endpoint Central Cloud"] != 3 {
		t.Errorf("groups = %v, want three groups with three rows under Endpoint Central Cloud", grouped)
	}
	first := rep.Components[0]
	if first.Name != "Endpoint Central" || first.Group != "Endpoint Central Cloud" {
		t.Errorf("first component = %+v", first)
	}
}

func TestStatusIQFetchOpenIssues(t *testing.T) {
	srv, adapter := serve(t, testPage, fixture(t, "statusiq_incidents.json"))

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL + "/", Kind: KindStatusIQ,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want major from the open outage", rep.Global)
	}

	if len(rep.Components) != 6 {
		t.Fatalf("components = %+v, want six", rep.Components)
	}
	cases := []struct {
		name   string
		group  string
		status adapters.Health
	}{
		{"API", "", adapters.HealthMajor},
		{"D Hanis", "Blue Ring", adapters.HealthMajor},
		{"Carrizo Springs", "Blue Ring", adapters.HealthOK},
		{"Docs", "", adapters.HealthMaintenance},
		{"Search", "", adapters.HealthDegraded},
		{"Release notes", "", adapters.HealthOK},
	}
	for i, tc := range cases {
		c := rep.Components[i]
		if c.Name != tc.name || c.Group != tc.group || c.Status != tc.status {
			t.Errorf("component %d = %+v, want %q in %q, %q", i, c, tc.name, tc.group, tc.status)
		}
	}

	// One open incident, two resolved and one scheduled maintenance.
	if len(rep.Incidents) != 4 {
		t.Fatalf("incidents = %+v, want four", rep.Incidents)
	}

	open := rep.Incidents[0]
	if open.SourceID != "active-api-outage" || open.Name != "API outage" {
		t.Errorf("open incident = %+v", open)
	}
	if open.Status != "acknowledged" || open.Impact != "major" || open.ResolvedAt != nil {
		t.Errorf("open incident = %+v, want acknowledged, major and unresolved", open)
	}
	if want := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC); !open.StartedAt.Equal(want) {
		t.Errorf("open incident started = %v, want %v", open.StartedAt, want)
	}

	build := rep.Incidents[1]
	if build.Name != "Build service degraded" || build.Status != "resolved" || build.Impact != "partial" {
		t.Errorf("build incident = %+v", build)
	}
	if build.ResolvedAt == nil {
		t.Error("build incident has no resolved time")
	} else if want := time.Date(2026, 11, 8, 10, 30, 0, 0, time.UTC); !build.ResolvedAt.Equal(want) {
		t.Errorf("build incident resolved = %v, want %v", build.ResolvedAt, want)
	}

	maintenance := rep.Incidents[2]
	if maintenance.Name != "Weekly maintenance" || maintenance.Status != "resolved" || maintenance.Impact != "maintenance" {
		t.Errorf("finished maintenance = %+v", maintenance)
	}

	upcoming := rep.Incidents[3]
	if upcoming.Name != "Nightly backup" || upcoming.Status != "scheduled" || upcoming.Impact != "maintenance" {
		t.Errorf("upcoming maintenance = %+v", upcoming)
	}
	if want := time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC); !upcoming.StartedAt.Equal(want) {
		t.Errorf("upcoming maintenance started = %v, want %v", upcoming.StartedAt, want)
	}
}

func TestStatusIQKeepsOnlyTheChosenComponents(t *testing.T) {
	srv, adapter := serve(t, testPage, fixture(t, "statusiq_incidents.json"))

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL + "/", Kind: KindStatusIQ, Include: []string{"api"},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 1 || rep.Components[0].Name != "API" {
		t.Errorf("components = %+v, want only API", rep.Components)
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want the state the page reports", rep.Global)
	}
}

func TestStatusIQRejectsPagesItCannotRead(t *testing.T) {
	srv, adapter := serve(t, `<html><body>not a status page</body></html>`, `{}`)
	if _, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL + "/", Kind: KindStatusIQ}); err == nil {
		t.Error("Fetch accepted a page without the StatusIQ id")
	}

	srv, adapter = serve(t, testPage, `{"code": 7, "message": "no such page"}`)
	if _, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL + "/", Kind: KindStatusIQ}); err == nil {
		t.Error("Fetch accepted an API error")
	}
}
