package betterstack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"downradar.info/backend/internal/adapters"
)

func betterstackFixture(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", "betterstack_index.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(body)
}

func serveBetterstack(t *testing.T, body string) (*httptest.Server, *Betterstack) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, NewBetterstack(srv.Client())
}

func TestBetterstackFetchRealFixture(t *testing.T) {
	srv, adapter := serveBetterstack(t, betterstackFixture(t))

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "sumopod", URL: srv.URL, Kind: KindBetterstack,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthDegraded {
		t.Errorf("Global = %q, want degraded, the state the page reports for itself", rep.Global)
	}
	if rep.PageID != "sumopod" {
		t.Errorf("PageID = %q, want the subdomain", rep.PageID)
	}
	if rep.SourceUpdatedAt.IsZero() {
		t.Error("SourceUpdatedAt is zero")
	}

	// The resource that is listed but not watched is left out, so two remain.
	if len(rep.Components) != 2 {
		t.Fatalf("components = %+v, want two", rep.Components)
	}
	first := rep.Components[0]
	if first.Name != "sumopod.com" || first.Group != "Dashboard" || first.Status != adapters.HealthDegraded {
		t.Errorf("first component = %+v", first)
	}
	if second := rep.Components[1]; second.Name != "JKT1" || second.Status != adapters.HealthOK {
		t.Errorf("second component = %+v", second)
	}

	if len(rep.Incidents) != 1 {
		t.Fatalf("incidents = %+v, want one", rep.Incidents)
	}
	inc := rep.Incidents[0]
	if inc.Name != "Degraded API in JKT1" || inc.Status != "resolved" || inc.Impact != "manual" {
		t.Errorf("incident = %+v", inc)
	}
	if inc.StartedAt.IsZero() || inc.ResolvedAt == nil {
		t.Errorf("incident times = %+v, want both ends", inc)
	}
}

func TestBetterstackKeepsOnlyTheChosenComponents(t *testing.T) {
	srv, adapter := serveBetterstack(t, betterstackFixture(t))

	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Kind: KindBetterstack, Include: []string{"jkt1"},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 || rep.Components[0].Name != "JKT1" {
		t.Fatalf("components = %+v, want only JKT1", rep.Components)
	}
}

// A page that names a state the adapter does not know still has to report
// something, and the worst resource is the closest thing to the truth.
func TestBetterstackFallsBackToTheWorstResource(t *testing.T) {
	body := `{
		"data": {"id": "1", "type": "status_page",
			"attributes": {"subdomain": "example", "aggregate_state": "something-new"}},
		"included": [
			{"id": "r1", "type": "status_page_resource",
				"attributes": {"public_name": "One", "status": "operational", "status_page_section_id": 0, "position": 0}},
			{"id": "r2", "type": "status_page_resource",
				"attributes": {"public_name": "Two", "status": "downtime", "status_page_section_id": 0, "position": 1}}
		]
	}`
	srv, adapter := serveBetterstack(t, body)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL, Kind: KindBetterstack})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want the worst resource", rep.Global)
	}
}

func TestBetterstackRefusesADocumentThatIsNotAPage(t *testing.T) {
	srv, adapter := serveBetterstack(t, `{"data":{"id":"","type":"other"}}`)

	if _, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL, Kind: KindBetterstack}); err == nil {
		t.Fatal("a document that is not a status page was read as one")
	}
}
