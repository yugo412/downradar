package adapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// solarwindsPage wraps a payload the way a page embeds it, with another script
// in front of it so the adapter has to pick the right one out.
func solarwindsPage(payload string) []byte {
	return []byte(`<!DOCTYPE html><html><head><script>window.__NEXT_P=window.__NEXT_P||[];</script></head>` +
		`<body><script id="__NEXT_DATA__" type="application/json">` + payload + `</script></body></html>`)
}

// solarwindsDetails wraps one details object in the rest of the envelope.
func solarwindsDetails(details string) []byte {
	return solarwindsPage(`{"props":{"pageProps":{"details":` + details + `}}}`)
}

func serveSolarwinds(t *testing.T, body []byte) (*httptest.Server, *Solarwinds) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, NewSolarwinds(srv.Client())
}

func TestSolarwindsFetchRealFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "solarwinds_flip.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv, adapter := serveSolarwinds(t, body)
	rep, err := adapter.Fetch(context.Background(), Target{Slug: "flip", URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != HealthOK {
		t.Errorf("Global = %q, want ok: the page states a state of its own", rep.Global)
	}
	if rep.PageID != "26af00d4b896b8e99319c1a0ded7cf98e7db701596473e975e7cabed2106c5d8" {
		t.Errorf("PageID = %q, want the hash the page carries", rep.PageID)
	}

	// Two groups and five children between them. A group is a heading rather
	// than a component, so it is not one of the five.
	if len(rep.Components) != 5 {
		t.Fatalf("components = %d, want 5 children", len(rep.Components))
	}

	byName := map[string]ComponentStatus{}
	for i, c := range rep.Components {
		byName[c.Name] = c
		if c.SourceID == "" {
			t.Errorf("%s: empty source id", c.Name)
		}
		if c.Position != i+1 {
			t.Errorf("%s: position = %d, want %d", c.Name, c.Position, i+1)
		}
	}
	if _, ok := byName["API"]; ok {
		t.Error("the group API is reported as a component")
	}
	if got := byName["Accept Payment"].Group; got != "API" {
		t.Errorf("Accept Payment: group = %q, want API", got)
	}
	if got := byName["Muamalat VA"].Group; got != "Money Transfer" {
		t.Errorf("Muamalat VA: group = %q, want Money Transfer", got)
	}
	if got := byName["Muamalat VA"].Status; got != HealthMajor {
		t.Errorf("Muamalat VA: status = %q, want major", got)
	}
	if got := byName["Accept Payment"].Status; got != HealthOK {
		t.Errorf("Accept Payment: status = %q, want ok", got)
	}

	// One resolved issue from the history, one still open, and two maintenance
	// windows: one planned and one the page calls completed.
	if len(rep.Incidents) != 4 {
		t.Fatalf("incidents = %d, want 4", len(rep.Incidents))
	}

	seen := map[string]bool{}
	bySource := map[string]Incident{}
	for _, inc := range rep.Incidents {
		if inc.SourceID == "" {
			t.Errorf("incident %q has no source id", inc.Name)
		}
		if seen[inc.SourceID] {
			t.Errorf("source id %q is used twice", inc.SourceID)
		}
		seen[inc.SourceID] = true
		bySource[inc.SourceID] = inc
		if inc.StartedAt.IsZero() {
			t.Errorf("incident %q has no start time", inc.Name)
		}
	}

	open := bySource["issue-10965"]
	if open.Name != "Out of Service For Money Transfer Transactions To Bank Jatim" || open.Status != "Investigating" {
		t.Errorf("open issue = %+v", open)
	}
	if open.ResolvedAt != nil {
		t.Error("an issue the page has not resolved must not carry an end time")
	}

	resolved := bySource["issue-39632"]
	if resolved.Status != "Resolved" {
		t.Errorf("resolved issue status = %q, want Resolved", resolved.Status)
	}
	if resolved.ResolvedAt == nil || resolved.ResolvedAt.UTC().Format(time.RFC3339) != "2026-10-16T20:02:00Z" {
		t.Errorf("resolved issue end = %v, want the time the page gives", resolved.ResolvedAt)
	}

	planned := bySource["maintenance-1895"]
	if planned.Status != "upcoming" {
		t.Errorf("planned maintenance status = %q, want upcoming", planned.Status)
	}
	if planned.ResolvedAt != nil {
		t.Error("a planned maintenance window must not carry an end time")
	}

	completed := bySource["maintenance-1879"]
	if completed.Status != "completed" {
		t.Errorf("completed maintenance status = %q, want completed", completed.Status)
	}
	if completed.ResolvedAt == nil || completed.ResolvedAt.UTC().Format(time.RFC3339) != "2026-10-16T23:45:00Z" {
		t.Errorf("completed maintenance end = %v, want its own end time", completed.ResolvedAt)
	}
}

func TestSolarwindsReadsComponentsWithoutAGroup(t *testing.T) {
	body := solarwindsDetails(`{
		"id": 9, "name": "Squadcast", "domainName": "abc",
		"status": {"id": 1, "slug": "operational"},
		"components": [
			{"id": 2, "type": "component", "name": "Web Application", "status": {"id": 1, "slug": "operational"}},
			{"id": 3, "type": "component", "name": "Platform API", "status": {"id": 1, "slug": "operational"}}
		]
	}`)

	srv, adapter := serveSolarwinds(t, body)
	rep, err := adapter.Fetch(context.Background(), Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(rep.Components))
	}
	for _, c := range rep.Components {
		if c.Group != "" {
			t.Errorf("%s: group = %q, want none", c.Name, c.Group)
		}
	}
	if rep.Global != HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
}

func TestSolarwindsMaintenanceBeatsTheComponentStatus(t *testing.T) {
	body := solarwindsDetails(`{
		"id": 9, "name": "X", "domainName": "abc",
		"status": {"id": 1, "slug": "operational"},
		"underMaintenance": "False",
		"components": [
			{"id": 2, "type": "component", "name": "Flagged",
			 "status": {"id": 1, "slug": "operational"}, "underMaintenance": "True"},
			{"id": 3, "type": "component", "name": "Only the code",
			 "status": {"id": 0, "slug": ""}, "statusMaintenance": {"id": 5, "slug": "under-maintenance"}}
		]
	}`)

	srv, adapter := serveSolarwinds(t, body)
	rep, err := adapter.Fetch(context.Background(), Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	got := map[string]Health{}
	for _, c := range rep.Components {
		got[c.Name] = c.Status
	}
	if got["Flagged"] != HealthMaintenance {
		t.Errorf("flagged component = %q, want maintenance", got["Flagged"])
	}
	if got["Only the code"] != HealthMaintenance {
		t.Errorf("component carrying only the maintenance code = %q, want maintenance", got["Only the code"])
	}
	// The page says it is operational, so two components under maintenance do
	// not move the state the page states.
	if rep.Global != HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}

	body = solarwindsDetails(`{
		"id": 9, "name": "X", "domainName": "abc",
		"status": {"id": 1, "slug": "operational"},
		"underMaintenance": "True",
		"components": []
	}`)

	srv, adapter = serveSolarwinds(t, body)
	rep, err = adapter.Fetch(context.Background(), Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != HealthMaintenance {
		t.Errorf("Global = %q, want maintenance while the page says it is under maintenance", rep.Global)
	}
}

func TestSolarwindsUnknownStateIsNotOk(t *testing.T) {
	body := solarwindsDetails(`{
		"id": 9, "name": "X", "domainName": "abc",
		"status": {"id": 1, "slug": "a brand new word"},
		"components": [
			{"id": 2, "type": "component", "name": "Web", "status": {"id": 1, "slug": "operational"}},
			{"id": 3, "type": "component", "name": "API", "status": {"id": 7, "slug": "brand_new_word"}}
		]
	}`)

	srv, adapter := serveSolarwinds(t, body)
	rep, err := adapter.Fetch(context.Background(), Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	for _, c := range rep.Components {
		if c.Name == "API" && c.Status != HealthUnknown {
			t.Errorf("API = %q, want unknown", c.Status)
		}
	}
	if rep.Global != HealthUnknown {
		t.Errorf("Global = %q, want unknown: neither the page nor a component names a known state", rep.Global)
	}
}

func TestSolarwindsIncludeFilter(t *testing.T) {
	body := solarwindsDetails(`{
		"id": 9, "name": "X", "domainName": "abc",
		"status": {"id": 1, "slug": "operational"},
		"components": [
			{"id": 1, "type": "group", "name": "Group", "components": [
				{"id": 2, "type": "component", "name": "Web", "status": {"id": 1, "slug": "operational"}},
				{"id": 3, "type": "component", "name": "API", "status": {"id": 3, "slug": "major-outage"}}
			]}
		]
	}`)

	srv, adapter := serveSolarwinds(t, body)
	rep, err := adapter.Fetch(context.Background(), Target{
		URL: srv.URL, Include: []string{"  api  "},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want only the one asked for", len(rep.Components))
	}
	if rep.Components[0].Name != "API" || rep.Components[0].Status != HealthMajor {
		t.Errorf("component = %+v", rep.Components[0])
	}
	if rep.Components[0].Position != 1 {
		t.Errorf("position = %d, want 1 after filtering", rep.Components[0].Position)
	}
}

func TestSolarwindsSkipsWhatThePageHides(t *testing.T) {
	body := solarwindsDetails(`{
		"id": 9, "name": "X", "domainName": "abc",
		"status": {"id": 1, "slug": "operational"},
		"components": [
			{"id": 1, "type": "group", "name": "Hidden", "isHidden": true, "components": [
				{"id": 2, "type": "component", "name": "Not shown", "status": {"id": 3, "slug": "major-outage"}}
			]},
			{"id": 3, "type": "group", "name": "Shown", "components": [
				{"id": 4, "type": "component", "name": "Web", "status": {"id": 1, "slug": "operational"}},
				{"id": 5, "type": "component", "name": "API", "isHidden": true, "status": {"id": 3, "slug": "major-outage"}}
			]}
		]
	}`)

	srv, adapter := serveSolarwinds(t, body)
	rep, err := adapter.Fetch(context.Background(), Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 1 || rep.Components[0].Name != "Web" {
		t.Fatalf("components = %+v, want only the one the page shows", rep.Components)
	}
	if rep.Global != HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
}

func TestSolarwindsRejects(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{"plain html", []byte("<html><body>just a page</body></html>"), "no __NEXT_DATA__"},
		{"next data without a page", solarwindsPage(`{"props":{"pageProps":{"details":{"name":"X"}}}}`), "not a SolarWinds status page"},
		{"broken json", solarwindsPage(`{"props":{"pageProps":`), "decode __NEXT_DATA__"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveSolarwinds(t, tc.body)
			_, err := adapter.Fetch(context.Background(), Target{URL: srv.URL})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestSolarwindsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	adapter := NewSolarwinds(srv.Client())
	rep, err := adapter.Fetch(context.Background(), Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error on HTTP 403")
	}
	if rep.HTTPStatus != http.StatusForbidden {
		t.Errorf("HTTPStatus = %d, want 403", rep.HTTPStatus)
	}
}
