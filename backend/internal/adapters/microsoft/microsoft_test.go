package microsoft

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

const testWorkloads = `[
  {"ServiceDisplayName": "Microsoft 365 (Consumer)", "ServiceWorkloadName": "M365Consumer",
   "LastUpdatedTime": "2026-10-28T03:11:00.5216149+00:00", "Status": "Operational"},
  {"ServiceDisplayName": "Microsoft Copilot", "ServiceWorkloadName": "CopilotConsumer",
   "LastUpdatedTime": "2026-10-28T03:10:00.5216176+00:00", "Status": "Service degradation"},
  {"ServiceDisplayName": "Microsoft Lists", "ServiceWorkloadName": "ConsumerList",
   "LastUpdatedTime": "2026-10-28T03:09:00.5216176+00:00", "Status": "Service restored"},
  {"ServiceDisplayName": "Microsoft Teams Free", "ServiceWorkloadName": "TeamsforLife",
   "LastUpdatedTime": "2026-10-28T03:08:00.5216189+00:00", "Status": "Unknown"},
  {"ServiceDisplayName": "OneDrive", "ServiceWorkloadName": "OneDriveConsumer",
   "LastUpdatedTime": "2026-10-28T03:07:00.5216191+00:00", "Status": "Maintenance"},
  {"ServiceDisplayName": "Outlook.com", "ServiceWorkloadName": "Outlook.com",
   "LastUpdatedTime": "2026-10-28T03:06:00.5216192+00:00", "Status": "Available"}
]`

func TestMicrosoftFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/posts/m365Consumer" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("empty User-Agent")
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		_, _ = fmt.Fprint(w, testWorkloads)
	}))
	defer srv.Close()

	rep, err := NewMicrosoft(srv.Client()).Fetch(context.Background(), adapters.Target{
		Slug: "microsoft-365", URL: srv.URL + "/m365",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthDegraded {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthDegraded)
	}
	if len(rep.Components) != 6 {
		t.Fatalf("components = %d, want 6", len(rep.Components))
	}

	cases := map[string]adapters.Health{
		"Microsoft 365 (Consumer)": adapters.HealthOK,
		"Microsoft Copilot":        adapters.HealthDegraded,
		"Microsoft Lists":          adapters.HealthOK,
		"Microsoft Teams Free":     adapters.HealthUnknown,
		"OneDrive":                 adapters.HealthMaintenance,
		"Outlook.com":              adapters.HealthOK,
	}
	for _, c := range rep.Components {
		if want, ok := cases[c.Name]; ok && c.Status != want {
			t.Errorf("%s: status = %q, want %q", c.Name, c.Status, want)
		}
	}

	first := rep.Components[0]
	if first.SourceID != "M365Consumer" {
		t.Errorf("SourceID = %q, want M365Consumer", first.SourceID)
	}
	if first.Position != 1 {
		t.Errorf("Position = %d, want 1", first.Position)
	}
	wantTime, err := time.Parse(time.RFC3339Nano, "2026-10-28T03:11:00.5216149+00:00")
	if err != nil {
		t.Fatal(err)
	}
	if !first.ChangedAt.Equal(wantTime) {
		t.Errorf("ChangedAt = %s, want %s", first.ChangedAt, wantTime)
	}
	if !rep.SourceUpdatedAt.Equal(wantTime) {
		t.Errorf("SourceUpdatedAt = %s, want %s", rep.SourceUpdatedAt, wantTime)
	}
}

func TestMicrosoftHonoursInclude(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, testWorkloads)
	}))
	defer srv.Close()

	rep, err := NewMicrosoft(srv.Client()).Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Include: []string{"onedrive"},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 || rep.Components[0].Name != "OneDrive" {
		t.Fatalf("components = %+v, want only OneDrive", rep.Components)
	}
	if rep.Global != adapters.HealthMaintenance {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthMaintenance)
	}
}

func TestMicrosoftRefusesAnEmptyPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	_, err := NewMicrosoft(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a page with no workloads")
	}
}

func TestMicrosoftReturnsTheHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	rep, err := NewMicrosoft(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a refusing page")
	}
	if rep.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTPStatus = %d, want %d", rep.HTTPStatus, http.StatusServiceUnavailable)
	}
}
