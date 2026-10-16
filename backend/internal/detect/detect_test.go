package detect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"downradar.info/backend/internal/catalog"
)

const statusioPageID = "5f2b3c4d5e6f708192a3b4c5"

func testCatalog(order ...string) catalog.Catalog {
	return catalog.Catalog{
		Profiles: catalog.Profiles{
			"statuspage-v2": {
				Kind: "statuspage", Endpoint: "/api/v2/summary.json",
				PollIntervalSeconds: 300, ActiveIntervalSeconds: 60,
			},
			"statusio": {
				Kind: "statusio", Endpoint: "/1.0/status/",
				PollIntervalSeconds: 300, ActiveIntervalSeconds: 60,
			},
			"sorryapp": {
				Kind: "sorryapp", Endpoint: "/api/v1/",
				PollIntervalSeconds: 300, ActiveIntervalSeconds: 60,
			},
		},
		Detect: order,
	}
}

// statuspageLike answers the way Atlassian Statuspage does.
func statuspageLike(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/summary.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"page": {"id": "sp-1", "name": "Example", "updated_at": "2024-05-01T10:00:00Z"},
			"status": {"indicator": "none"},
			"components": [{"id": "c1", "name": "API", "status": "operational", "position": 1}]
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// statusioLike answers the way Status.io does: an id on the page, then the API.
func statusioLike(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<script>var statuspage_id = "` + statusioPageID + `";</script>`))
		case strings.HasPrefix(r.URL.Path, "/1.0/status/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"status_overall":{"status_code":100,"updated":"2024-05-01T10:00:00Z"},"status":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// sorryLike answers the way Sorry does.
func sorryLike(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/status":
			_, _ = w.Write([]byte(`{"page":{"id":7,"name":"Example","state":"operational","updated_at":"2024-05-01T10:00:00Z"}}`))
		case "/api/v1/components":
			_, _ = w.Write([]byte(`{"components":[{"id":1,"name":"API","state":"operational","position":1}]}`))
		case "/api/v1/notices":
			_, _ = w.Write([]byte(`{"notices":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDetectWalksTheQueueInOrder(t *testing.T) {
	// A Status.io page with Statuspage first in the queue: the first profile
	// cannot read it, so the second one has to.
	d := New(testCatalog("statuspage-v2", "statusio", "sorryapp"))

	match, err := d.Detect(context.Background(), "Example", statusioLike(t).URL)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if match.Profile != "statusio" {
		t.Errorf("profile = %q, want statusio", match.Profile)
	}
	if match.Report.PageID != statusioPageID {
		t.Errorf("page id = %q, want the one from the page", match.Report.PageID)
	}
	if match.Report.Global != "ok" {
		t.Errorf("global = %q, want ok", match.Report.Global)
	}
}

func TestDetectReachesTheLastProfile(t *testing.T) {
	d := New(testCatalog("statuspage-v2", "statusio", "sorryapp"))

	match, err := d.Detect(context.Background(), "Pingdom", sorryLike(t).URL)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if match.Profile != "sorryapp" {
		t.Errorf("profile = %q, want sorryapp", match.Profile)
	}
	if match.Report.PageID != "7" {
		t.Errorf("page id = %q, want 7", match.Report.PageID)
	}
}

func TestDetectTakesTheFirstMatchWhateverComesAfter(t *testing.T) {
	d := New(testCatalog("sorryapp", "statuspage-v2", "statusio"))

	match, err := d.Detect(context.Background(), "Example", statuspageLike(t).URL)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if match.Profile != "statuspage-v2" {
		t.Errorf("profile = %q, want the queue order to decide", match.Profile)
	}
	if len(match.Report.Components) != 1 {
		t.Errorf("components = %d, want the report that matched", len(match.Report.Components))
	}
}

func TestDetectReportsWhatEveryProfileMadeOfIt(t *testing.T) {
	// A page that answers nothing: every profile has to say why it gave up.
	blank := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(blank.Close)

	d := New(testCatalog("statuspage-v2", "statusio", "sorryapp"))

	_, err := d.Detect(context.Background(), "Example", blank.URL+"/unknown")
	if err == nil {
		t.Fatal("expected no profile to match")
	}

	var noMatch *NoMatch
	if !errors.As(err, &noMatch) {
		t.Fatalf("error = %T, want *NoMatch", err)
	}
	if len(noMatch.Attempts) != 3 {
		t.Fatalf("attempts = %d, want one per profile", len(noMatch.Attempts))
	}
	for i, want := range []string{"statuspage-v2", "statusio", "sorryapp"} {
		if noMatch.Attempts[i].Profile != want {
			t.Errorf("attempt %d = %q, want %q", i, noMatch.Attempts[i].Profile, want)
		}
		if noMatch.Attempts[i].Err == nil {
			t.Errorf("attempt %d carries no reason", i)
		}
	}

	message := err.Error()
	for _, name := range []string{"statuspage-v2", "statusio", "sorryapp"} {
		if !strings.Contains(message, name) {
			t.Errorf("message %q does not mention %q", message, name)
		}
	}
}

func TestDetectWithAnEmptyQueue(t *testing.T) {
	d := New(testCatalog())

	_, err := d.Detect(context.Background(), "Example", "https://status.example.com")
	var noMatch *NoMatch
	if !errors.As(err, &noMatch) {
		t.Fatalf("error = %v, want *NoMatch", err)
	}
	if len(noMatch.Attempts) != 0 {
		t.Errorf("attempts = %d, want none", len(noMatch.Attempts))
	}
}

func TestCandidatesSkipProfilesThatAreNotDeclared(t *testing.T) {
	cat := testCatalog("statuspage-v2", "gone")
	cat.Detect = append(cat.Detect, "statusio")

	candidates := cat.Candidates()
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want the two that exist", len(candidates))
	}
	if candidates[0].Name != "statuspage-v2" || candidates[1].Name != "statusio" {
		t.Errorf("candidates = %v, want them in queue order", candidates)
	}
}
