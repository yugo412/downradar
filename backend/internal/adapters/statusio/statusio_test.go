package statusio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"downradar.info/backend/internal/adapters"
)

const statusioTestPageID = "51f6f2088643809b7200000d"

func statusioPage(id string) string {
	return `<html><script>(function(){var statuspageId = '` + id + `';})()</script></html>`
}

func serveStatusio(t *testing.T, page, api string) (*httptest.Server, *Statusio) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(page))
		case "/1.0/status/" + statusioTestPageID:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(api))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, NewStatusio(srv.Client())
}

func TestStatusioFetchRealFixture(t *testing.T) {
	api, err := os.ReadFile(filepath.Join("testdata", "statusio_status.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv, adapter := serveStatusio(t, statusioPage(statusioTestPageID), string(api))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "status-io", URL: srv.URL, Kind: KindStatusio,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
	if rep.PageID != statusioTestPageID {
		t.Errorf("PageID = %q, want %q: the id read from the page is the identity", rep.PageID, statusioTestPageID)
	}
	if rep.SourceUpdatedAt.IsZero() {
		t.Error("SourceUpdatedAt is zero")
	}

	// The fixture holds six groups and twenty nine containers. Groups are not
	// reported on their own, because their status is a rollup of the containers.
	if len(rep.Components) != 29 {
		t.Fatalf("components = %d, want 29 containers", len(rep.Components))
	}

	first := rep.Components[0]
	if first.Name != "US-East" || first.Group != "Public Status Pages" {
		t.Errorf("first component = %q in group %q", first.Name, first.Group)
	}
	if first.SourceID == "" || first.Position != 1 || first.Status != adapters.HealthOK {
		t.Errorf("first component = %+v", first)
	}

	for _, c := range rep.Components {
		if c.Group == "" {
			t.Errorf("component %q carries no group", c.Name)
		}
		if c.Status != adapters.HealthOK {
			t.Errorf("component %q = %q, want ok", c.Name, c.Status)
		}
	}
}

func TestStatusioFindsThePageIDInEverySpelling(t *testing.T) {
	pages := map[string]string{
		"statuspageId assignment": `<script>var statuspageId = '` + statusioTestPageID + `';</script>`,
		"pageId assignment":       `<script>var pageId = "` + statusioTestPageID + `";</script>`,
		"page_id property":        `<script>{ page_id: "` + statusioTestPageID + `" }</script>`,
		"statuspage_id property":  `<script>{ statuspage_id: '` + statusioTestPageID + `' }</script>`,
	}

	for name, page := range pages {
		t.Run(name, func(t *testing.T) {
			srv, adapter := serveStatusio(t, page, `{"result":{"status_overall":{"status_code":100}}}`)
			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if rep.PageID != statusioTestPageID {
				t.Errorf("PageID = %q, want %q", rep.PageID, statusioTestPageID)
			}
		})
	}
}

func TestStatusioWithoutAPageID(t *testing.T) {
	srv, adapter := serveStatusio(t, `<html>nothing to see</html>`, `{}`)

	_, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error when the page carries no id")
	}
	if !strings.Contains(err.Error(), "no page id") {
		t.Errorf("error = %q, want it to mention the missing page id", err.Error())
	}
}

func TestStatusioCodeVocabulary(t *testing.T) {
	cases := map[int]adapters.Health{
		100: adapters.HealthOK,
		200: adapters.HealthMaintenance,
		300: adapters.HealthDegraded,
		400: adapters.HealthPartial,
		500: adapters.HealthMajor,
		600: adapters.HealthMajor,
		999: adapters.HealthUnknown,
	}

	for code, want := range cases {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			srv, adapter := serveStatusio(t, statusioPage(statusioTestPageID),
				`{"result":{"status_overall":{"status_code":`+strconv.Itoa(code)+`}}}`)

			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if rep.Global != want {
				t.Errorf("Global = %q, want %q", rep.Global, want)
			}
		})
	}
}

func TestStatusioIncludeFilter(t *testing.T) {
	api := `{"result":{"status_overall":{"status_code":300},"status":[{"name":"Group","containers":[` +
		`{"id":"a","name":"US-East","status_code":100},` +
		`{"id":"b","name":"US-West","status_code":300}]}]}}`

	srv, adapter := serveStatusio(t, statusioPage(statusioTestPageID), api)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Include: []string{"  us-west  "},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want only the one asked for", len(rep.Components))
	}
	if rep.Components[0].Name != "US-West" || rep.Components[0].Status != adapters.HealthDegraded {
		t.Errorf("component = %+v", rep.Components[0])
	}
	if rep.Components[0].Position != 1 {
		t.Errorf("position = %d, want 1 after filtering", rep.Components[0].Position)
	}
}

func TestStatusioRejects(t *testing.T) {
	cases := []struct {
		name string
		api  string
		want string
	}{
		{"no status_overall", `{"result":{"status":[]}}`, "no status_overall"},
		{"not json at all", `<html>nope</html>`, "decode json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveStatusio(t, statusioPage(statusioTestPageID), tc.api)
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

func TestStatusioHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	adapter := NewStatusio(srv.Client())
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error on HTTP 403")
	}
	if rep.HTTPStatus != http.StatusForbidden {
		t.Errorf("HTTPStatus = %d, want 403", rep.HTTPStatus)
	}
}
