package cronjob

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"downradar.info/backend/internal/adapters"
)

func cronjobBody(monitors ...string) []byte {
	return []byte(`{"statusPage":{"statusPageMeta":{"title":"Test"},"statusPageMonitors":[` +
		strings.Join(monitors, ",") + `]}}`)
}

func monitorJSON(title string, enabled bool, buckets string) string {
	return fmt.Sprintf(
		`{"title":%q,"enabled":%t,"percentile":0.9,`+
			`"thresholds":{"uptime":{"error":0.5,"warning":0.9},`+
			`"latency":{"error":20000,"warning":2000}},`+
			`"timeSeriesData":{"last24Hours":%s}}`,
		title, enabled, buckets,
	)
}

func serveCronJob(t *testing.T, body []byte) (*httptest.Server, *CronJob) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Method"); got != "GetPublicStatusPage" {
			t.Errorf("X-API-Method = %q, want GetPublicStatusPage", got)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, NewCronJob(srv.Client())
}

func TestCronJobFetchRealFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "cronjob_schedules_run.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv, adapter := serveCronJob(t, body)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "schedules-run", URL: "https://status.schedules.run", Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(rep.Components))
	}
	if rep.Components[0].Name != "Ping" {
		t.Errorf("component = %q, want Ping", rep.Components[0].Name)
	}
	if rep.Components[0].Status != adapters.HealthOK {
		t.Errorf("status = %q, want ok", rep.Components[0].Status)
	}
	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
}

func TestCronJobSendsPageDomain(t *testing.T) {
	var gotDomain string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Domain string `json:"domain"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotDomain = body.Domain
		_, _ = w.Write(cronjobBody(monitorJSON("Ping", true, "[[1,100,3,3]]")))
	}))
	defer srv.Close()

	adapter := NewCronJob(srv.Client())
	_, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: "https://status.schedules.run", Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotDomain != "status.schedules.run" {
		t.Errorf("domain = %q, want status.schedules.run", gotDomain)
	}
}

func TestCronJobStatusFromThresholds(t *testing.T) {
	cases := []struct {
		name    string
		buckets string
		want    adapters.Health
	}{
		{"healthy", "[[1,100,3,3],[2,110,3,3]]", adapters.HealthOK},
		{"uptime below error", "[[1,100,1,3],[2,110,1,3]]", adapters.HealthMajor},
		{"uptime between warning and error", "[[1,100,2,3],[2,110,2,3]]", adapters.HealthPartial},
		{"latency above error", "[[1,25000,3,3],[2,26000,3,3]]", adapters.HealthDegraded},
		{"latency above warning", "[[1,5000,3,3],[2,6000,3,3]]", adapters.HealthDegraded},
		{"uptime worse than latency", "[[1,25000,1,3],[2,26000,1,3]]", adapters.HealthMajor},
		{"no data at all", "[[1,-1,0,0],[2,-1,0,0]]", adapters.HealthUnknown},
		{"latency missing but uptime fine", "[[1,-1,3,3],[2,-1,3,3]]", adapters.HealthOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveCronJob(t, cronjobBody(monitorJSON("Ping", true, tc.buckets)))
			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: "https://status.example.com", Endpoint: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if got := rep.Components[0].Status; got != tc.want {
				t.Errorf("component status = %q, want %q", got, tc.want)
			}
			if rep.Global != tc.want {
				t.Errorf("Global = %q, want %q", rep.Global, tc.want)
			}
		})
	}
}

func TestCronJobNoDataIsNeverOk(t *testing.T) {
	srv, adapter := serveCronJob(t, cronjobBody(
		monitorJSON("Healthy", true, "[[1,100,3,3],[2,110,3,3]]"),
		monitorJSON("Silent", true, "[[1,-1,0,0],[2,-1,0,0]]"),
	))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: "https://status.example.com", Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Components[1].Status != adapters.HealthUnknown {
		t.Errorf("silent monitor = %q, want unknown", rep.Components[1].Status)
	}
	if rep.Global == adapters.HealthOK {
		t.Error("global must not be ok while a monitor has no data")
	}
}

func TestCronJobSkipsDisabledMonitors(t *testing.T) {
	srv, adapter := serveCronJob(t, cronjobBody(
		monitorJSON("Enabled", true, "[[1,100,3,3]]"),
		monitorJSON("Disabled", false, "[[1,100,0,3]]"),
	))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: "https://status.example.com", Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 || rep.Components[0].Name != "Enabled" {
		t.Errorf("components = %+v, want only Enabled", rep.Components)
	}
}

func TestCronJobRejects(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{"no monitors", cronjobBody(), "no monitors"},
		{"all disabled", cronjobBody(monitorJSON("Off", false, "[[1,100,3,3]]")), "no enabled monitors"},
		{"not json", []byte("<html>nope</html>"), "decode"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveCronJob(t, tc.body)
			_, err := adapter.Fetch(context.Background(), adapters.Target{URL: "https://status.example.com", Endpoint: srv.URL})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestCronJobRejectsURLWithoutHost(t *testing.T) {
	_, adapter := serveCronJob(t, cronjobBody(monitorJSON("Ping", true, "[[1,100,3,3]]")))
	if _, err := adapter.Fetch(context.Background(), adapters.Target{URL: "not-a-url"}); err == nil {
		t.Fatal("expected an error for a url without a host")
	}
}

func TestPercentileOf(t *testing.T) {
	cases := []struct {
		in   []float64
		p    float64
		want float64
	}{
		{nil, 0.9, -1},
		{[]float64{42}, 0.9, 42},
		{[]float64{983, 1081}, 0.9, 983 + (1081-983)*0.8},
		{[]float64{1081, 983}, 0.9, 983 + (1081-983)*0.8},
		{[]float64{1, 2, 3, 4, 5}, 0.9, 4.5},
	}
	for _, tc := range cases {
		got := percentileOf(tc.in, tc.p)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("percentileOf(%v, %v) = %v, want %v", tc.in, tc.p, got, tc.want)
		}
	}
}

func TestMeanOf(t *testing.T) {
	if got := meanOf(nil); got != -1 {
		t.Errorf("meanOf(nil) = %v, want -1", got)
	}
	if got := meanOf([]float64{1, 1, 0.5}); math.Abs(got-0.8333333333333334) > 1e-9 {
		t.Errorf("meanOf = %v", got)
	}
}

func TestUptimeRatiosAndLatencies(t *testing.T) {
	buckets := [][]float64{
		{1, -1, 3, 3},
		{2, 100, 0, 0},
		{3, 200, 1, 4},
	}
	ratios := uptimeRatios(buckets)
	if len(ratios) != 2 || ratios[0] != 1 || ratios[1] != 0.25 {
		t.Errorf("uptimeRatios = %v, want [1 0.25]", ratios)
	}
	lats := latencies(buckets)
	if len(lats) != 2 || lats[0] != 100 || lats[1] != 200 {
		t.Errorf("latencies = %v, want [100 200]", lats)
	}
}

func TestLastBuckets(t *testing.T) {
	all := [][]float64{{1}, {2}, {3}, {4}}
	got := lastBuckets(all, 2)
	if len(got) != 2 || got[0][0] != 3 || got[1][0] != 4 {
		t.Errorf("lastBuckets = %v", got)
	}
	if got := lastBuckets([][]float64{{1}}, 5); len(got) != 1 {
		t.Errorf("lastBuckets should return everything when n exceeds the length")
	}
}

func TestPageDomain(t *testing.T) {
	cases := map[string]string{
		"https://status.schedules.run": "status.schedules.run",
		"https://status.cron-job.org/": "status.cron-job.org",
		"http://example.com/path?q=1":  "example.com",
	}
	for in, want := range cases {
		got, err := pageDomain(in)
		if err != nil {
			t.Errorf("pageDomain(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("pageDomain(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := pageDomain("not-a-url"); err == nil {
		t.Error("expected an error")
	}
}
