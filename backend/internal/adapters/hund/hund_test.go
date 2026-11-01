package hund

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

func serve(t *testing.T, body string) (*httptest.Server, *Hund) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, NewHund(srv.Client())
}

func TestHundFetchRealFixture(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("testdata", "hund_status.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv, adapter := serve(t, string(page))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "bitwarden", URL: srv.URL, Kind: KindHund,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthDegraded {
		t.Errorf("Global = %q, want degraded", rep.Global)
	}
	if rep.PageID == "" {
		t.Error("PageID is empty: the page host is the identity")
	}
	if got, want := rep.SourceUpdatedAt, time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("SourceUpdatedAt = %s, want %s", got, want)
	}

	if len(rep.Components) != 3 {
		t.Fatalf("components = %d, want 3", len(rep.Components))
	}

	byName := make(map[string]adapters.ComponentStatus, len(rep.Components))
	for _, c := range rep.Components {
		byName[c.Name] = c
	}
	cases := []struct {
		name   string
		group  string
		status adapters.Health
	}{
		{"Identity Service", "Core", adapters.HealthDegraded},
		{"Web & Vault", "Core", adapters.HealthOK},
		{"RESTful API", "Data", adapters.HealthMajor},
	}
	for _, tc := range cases {
		c, ok := byName[tc.name]
		if !ok {
			t.Errorf("component %q is missing", tc.name)
			continue
		}
		if c.Group != tc.group || c.Status != tc.status {
			t.Errorf("%s = group %q status %q, want group %q status %q",
				tc.name, c.Group, c.Status, tc.group, tc.status)
		}
		if c.SourceID == "" || c.Position == 0 {
			t.Errorf("%s = %+v, want a source id and a position", tc.name, c)
		}
	}

	if len(rep.Incidents) != 4 {
		t.Fatalf("incidents = %d, want the open notice and three from history", len(rep.Incidents))
	}

	open := rep.Incidents[0]
	if open.SourceID != "60b70532f02f4659fa23f0b8" || open.Name != "Investigating login issues" {
		t.Errorf("open incident = %+v", open)
	}
	if open.Status != "investigating" || open.ResolvedAt != nil {
		t.Errorf("open incident status = %q, resolved = %v, want investigating and unresolved",
			open.Status, open.ResolvedAt)
	}
	if want := time.Date(2026, 10, 31, 11, 12, 34, 0, time.UTC); !open.StartedAt.Equal(want) {
		t.Errorf("open incident started = %s, want %s", open.StartedAt, want)
	}
	if !strings.HasSuffix(open.URL, "/issues/60b70532f02f4659fa23f0b8") {
		t.Errorf("open incident url = %q, want it resolved against the page", open.URL)
	}

	resolved := rep.Incidents[1]
	if resolved.Name != "Elevated errors" || resolved.Status != "resolved" {
		t.Errorf("resolved incident = %+v", resolved)
	}
	if resolved.ResolvedAt == nil {
		t.Fatal("resolved incident has no end")
	}
	if want := time.Date(2026, 9, 30, 3, 50, 3, 0, time.UTC); !resolved.ResolvedAt.Equal(want) {
		t.Errorf("resolved incident ended = %s, want %s", resolved.ResolvedAt, want)
	}

	unlinked := rep.Incidents[3]
	if unlinked.Name != "Degraded Service" || unlinked.Status != "resolved" || unlinked.Impact != "degraded" {
		t.Errorf("unlinked incident = %+v", unlinked)
	}
	if unlinked.SourceID == "" {
		t.Error("unlinked incident has no source id")
	}
	if unlinked.URL != "" {
		t.Errorf("unlinked incident url = %q, want no link", unlinked.URL)
	}
	if unlinked.ResolvedAt == nil {
		t.Fatal("unlinked incident has no end")
	}
}

func TestHundStateVocabulary(t *testing.T) {
	cases := map[string]adapters.Health{
		"operational": adapters.HealthOK,
		"degraded":    adapters.HealthDegraded,
		"partial":     adapters.HealthPartial,
		"outage":      adapters.HealthMajor,
		"maintenance": adapters.HealthMaintenance,
		"pending":     adapters.HealthMaintenance,
	}

	for class, want := range cases {
		t.Run(class, func(t *testing.T) {
			srv, adapter := serve(t, `<!DOCTYPE html><html><body>`+
				`<aside class='`+class+` state-bar'><span class='status'>`+class+`</span></aside>`+
				`<section class='groups'><section class='group'><header><h2>Group</h2></header>`+
				`<div class='item'><div class='info'><span class='name'>`+
				`<a href="/components/one">Thing</a></span></div>`+
				`<div class='status'><span class='`+class+` state icon' title='`+class+`'></span></div></div>`+
				`</section></section></body></html>`)

			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if rep.Global != want {
				t.Errorf("Global = %q, want %q", rep.Global, want)
			}
			if len(rep.Components) != 1 || rep.Components[0].Status != want {
				t.Errorf("components = %+v, want one %q", rep.Components, want)
			}
		})
	}
}

func TestHundReadsTheLabelWhenTheClassIsUnknown(t *testing.T) {
	srv, adapter := serve(t, `<!DOCTYPE html><html><body>`+
		`<aside class='warning state-bar'><span class='status'>Experiencing Downtime</span></aside>`+
		`<section class='groups'><section class='group'><header><h2>Group</h2></header>`+
		`<div class='item'><div class='info'><span class='name'>`+
		`<a href="/components/one">Thing</a></span></div>`+
		`<div class='status'><span class='warning state icon' title='Degraded Performance'></span></div></div>`+
		`</section></section></body></html>`)

	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want major from the label", rep.Global)
	}
	if len(rep.Components) != 1 || rep.Components[0].Status != adapters.HealthDegraded {
		t.Errorf("components = %+v, want the state title to be read", rep.Components)
	}
}

func TestHundIncludeFilter(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("testdata", "hund_status.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv, adapter := serve(t, string(page))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Include: []string{"  web & vault  "},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want only the one asked for", len(rep.Components))
	}
	if rep.Components[0].Name != "Web & Vault" {
		t.Errorf("component = %+v", rep.Components[0])
	}
}

func TestHundRefusesAnotherPage(t *testing.T) {
	srv, adapter := serve(t, `<!DOCTYPE html><html><body><p>not a status page</p></body></html>`)

	_, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a page with no state bar or groups")
	}
	if !strings.Contains(err.Error(), "not a Hund.io page") {
		t.Errorf("error = %q, want it to name the page", err.Error())
	}
}

func TestHundHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	rep, err := NewHund(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error on HTTP 403")
	}
	if rep.HTTPStatus != http.StatusForbidden {
		t.Errorf("HTTPStatus = %d, want 403", rep.HTTPStatus)
	}
}
