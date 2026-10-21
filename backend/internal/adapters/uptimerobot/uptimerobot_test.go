package uptimerobot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"downradar.info/backend/internal/adapters"
)

const testPage = `<!doctype html><html><body>
<script>fetch("/api/getMonitorList/K6wgs1Eg")</script>
</body></html>`

const testMonitors = `{
  "status": "ok",
  "data": [
    {"monitorId": 1, "name": "packagist.org", "statusClass": "success", "groupName": "Monitors (default)"},
    {"monitorId": 2, "name": "API", "statusClass": "danger", "groupName": "Monitors (default)"},
    {"monitorId": 3, "name": "Docs", "statusClass": "paused", "groupName": "Monitors (default)"}
  ]
}`

func TestUptimeRobotFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = fmt.Fprint(w, testPage)
		case "/api/getMonitorList/K6wgs1Eg":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, testMonitors)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("empty User-Agent")
		}
	}))
	defer srv.Close()

	rep, err := NewUptimeRobot(srv.Client()).Fetch(context.Background(), adapters.Target{
		Slug: "packagist", URL: srv.URL + "/",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.PageID != "K6wgs1Eg" {
		t.Errorf("PageID = %q, want K6wgs1Eg", rep.PageID)
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthMajor)
	}
	if len(rep.Components) != 3 {
		t.Fatalf("components = %d, want 3", len(rep.Components))
	}
	cases := map[string]adapters.Health{
		"packagist.org": adapters.HealthOK,
		"API":           adapters.HealthMajor,
		"Docs":          adapters.HealthMaintenance,
	}
	for _, c := range rep.Components {
		if want, ok := cases[c.Name]; ok && c.Status != want {
			t.Errorf("%s: status = %q, want %q", c.Name, c.Status, want)
		}
	}
}

func TestUptimeRobotRefusesAnotherPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>not an uptimerobot page</body></html>`)
	}))
	defer srv.Close()

	_, err := NewUptimeRobot(srv.Client()).Fetch(context.Background(), adapters.Target{
		Slug: "somewhere", URL: srv.URL,
	})
	if err == nil {
		t.Fatal("Fetch accepted a page with no monitor list")
	}
}
