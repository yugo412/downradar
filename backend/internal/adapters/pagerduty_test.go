package adapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func pagerdutyFixture(t *testing.T, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", "pagerduty_"+name+".json"))
	if err != nil {
		t.Fatalf("read %s fixture: %v", name, err)
	}
	return string(body)
}

func servePagerduty(t *testing.T, impacted, services, enums, posts string) (*httptest.Server, *Pagerduty) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case "/api/impacted_services":
			body = impacted
		case "/api/services":
			body = services
		case "/api/post_enums":
			body = enums
		case "/api/posts":
			// The history is read a window at a time, so the page is asked for
			// a range of millisecond timestamps rather than for everything.
			since, errSince := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
			until, errUntil := strconv.ParseInt(r.URL.Query().Get("until"), 10, 64)
			if errSince != nil || errUntil != nil || until <= since ||
				until-since > int64((31*24*time.Hour).Milliseconds()) {
				http.Error(w, "not a window of millisecond timestamps", http.StatusBadRequest)
				return
			}
			body = posts
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, NewPagerduty(srv.Client())
}

func TestPagerdutyFetchRealFixture(t *testing.T) {
	srv, adapter := servePagerduty(t,
		pagerdutyFixture(t, "impacted_services"),
		pagerdutyFixture(t, "services"),
		pagerdutyFixture(t, "post_enums"),
		pagerdutyFixture(t, "posts"),
	)

	// The url is a path into the page, the way a reader would have one open,
	// and the API is still what gets read.
	rep, err := adapter.Fetch(context.Background(), Target{
		Slug: "pagerduty", URL: srv.URL + "/posts/dashboard", Kind: KindPagerduty,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.PageID != "P67C5DQ" {
		t.Errorf("PageID = %q, want P67C5DQ: the page id is the identity to pin", rep.PageID)
	}
	if rep.HTTPStatus != http.StatusOK {
		t.Errorf("HTTPStatus = %d, want 200", rep.HTTPStatus)
	}
	if len(rep.Raw) == 0 {
		t.Error("Raw is empty, so there is nothing to keep of what the page said")
	}
	if rep.Global != HealthPartial {
		t.Errorf("Global = %q, want partial: one service is in trouble", rep.Global)
	}

	want := []struct {
		id, name string
		status   Health
	}{
		{"PFSPYI7", "Runbook Automation", HealthOK},
		{"PJ1AZXX", "Workflow Automation", HealthPartial},
		{"PDACBTY", "Events API (US)", HealthOK},
	}
	if len(rep.Components) != len(want) {
		t.Fatalf("components = %d, want %d", len(rep.Components), len(want))
	}
	for i, w := range want {
		got := rep.Components[i]
		if got.SourceID != w.id || got.Name != w.name {
			t.Errorf("component %d = %q/%q, want %q/%q", i, got.SourceID, got.Name, w.id, w.name)
		}
		if got.Status != w.status {
			t.Errorf("component %q = %q, want %q", w.name, got.Status, w.status)
		}
		if got.Position != i+1 {
			t.Errorf("component %q position = %d, want %d", w.name, got.Position, i+1)
		}
	}

	if len(rep.Incidents) != 2 {
		t.Fatalf("incidents = %d, want 2", len(rep.Incidents))
	}

	open := rep.Incidents[1]
	if open.SourceID != "PB7XK2M" || open.Name != "Elevated error rates on the Events API" {
		t.Errorf("incident = %q/%q, want the Events API one", open.SourceID, open.Name)
	}
	if open.Status != "investigating" || open.Impact != "major" {
		t.Errorf("incident = %q/%q, want the names the page uses for its own states",
			open.Status, open.Impact)
	}
	if open.ResolvedAt != nil {
		t.Errorf("an investigating post is not resolved, got %s", open.ResolvedAt)
	}
	if want := time.UnixMilli(1792116000000).UTC(); !open.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %s, want %s", open.StartedAt, want)
	}

	resolved := rep.Incidents[0]
	if resolved.Status != "resolved" || resolved.Impact != "minor" {
		t.Errorf("incident = %q/%q, want resolved/minor", resolved.Status, resolved.Impact)
	}
	if resolved.ResolvedAt == nil {
		t.Fatal("a post whose latest update is resolved carries no resolution time")
	}
	if want := time.UnixMilli(1788478164000).UTC(); !resolved.ResolvedAt.Equal(want) {
		t.Errorf("ResolvedAt = %s, want the time of the latest update %s", resolved.ResolvedAt, want)
	}
	if want := srv.URL + "/posts/details/PX7KMD4"; resolved.URL != want {
		t.Errorf("URL = %q, want %q", resolved.URL, want)
	}
}

func TestPagerdutyFetchRejectsOtherPages(t *testing.T) {
	// Anything answering at these paths that is not a PagerDuty page has no
	// status_page_id to pin, which is what tells the queue this is not a match.
	srv, adapter := servePagerduty(t, `{"services":[]}`, "", "", "")

	_, err := adapter.Fetch(context.Background(), Target{URL: srv.URL, Kind: KindPagerduty})
	if err == nil {
		t.Fatal("Fetch accepted a page with no status_page_id")
	}
	if !strings.Contains(err.Error(), "not a PagerDuty status page") {
		t.Errorf("Fetch error = %v, want it to say the page is not one", err)
	}
}

func TestPagerdutyUnknownImpactVocabulary(t *testing.T) {
	// Pages can word their own table. A name the adapter does not know leaves
	// the component unknown rather than guessed, and the fetch log lists it.
	srv, adapter := servePagerduty(t,
		`{"status_page_id":"PTEST1","impacted_services":[{"service_id":"PA1","impact_severity_id":"PX9"}]}`,
		`{"services":[{"id":"PA1","name":"Dashboard","display_name":"Dashboard","is_active":true}]}`,
		`{"post_enums":[{"id":"PX9","name":"queue congestion","is_featured":true}]}`,
		`{"posts":[]}`,
	)

	rep, err := adapter.Fetch(context.Background(), Target{URL: srv.URL, Kind: KindPagerduty})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(rep.Components))
	}
	if rep.Components[0].Status != HealthUnknown {
		t.Errorf("component status = %q, want unknown for an unmapped impact", rep.Components[0].Status)
	}
	if rep.Global != HealthUnknown {
		t.Errorf("Global = %q, want unknown", rep.Global)
	}
}

func TestPagerdutySkipsServicesThePageStoppedShowing(t *testing.T) {
	srv, adapter := servePagerduty(t,
		`{"status_page_id":"PTEST1","impacted_services":[]}`,
		`{"services":[{"id":"PA1","name":"Dashboard","display_name":"Dashboard","is_active":true},`+
			`{"id":"PA2","name":"Retired","display_name":"Retired","is_active":false}]}`,
		`{"post_enums":[]}`,
		`{"posts":[]}`,
	)

	rep, err := adapter.Fetch(context.Background(), Target{URL: srv.URL, Kind: KindPagerduty})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 1 || rep.Components[0].SourceID != "PA1" {
		t.Errorf("components = %+v, want only the service the page still shows", rep.Components)
	}
	if rep.Global != HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
}
