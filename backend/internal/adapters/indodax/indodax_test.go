package indodax

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"downradar.info/backend/internal/adapters"
)

func serveIndodax(t *testing.T, body string) (*httptest.Server, *Indodax) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/html" {
			t.Errorf("Accept = %q, want text/html", r.Header.Get("Accept"))
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("empty User-Agent")
		}
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, NewIndodax(srv.Client())
}

func realPage(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", "indodax_status.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(body)
}

func TestIndodaxFetchRealFixture(t *testing.T) {
	srv, adapter := serveIndodax(t, realPage(t))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "indodax", URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthMaintenance {
		t.Errorf("Global = %q, want maintenance from the worst row", rep.Global)
	}
	if len(rep.Components) != 18 {
		t.Fatalf("components = %d, want 18", len(rep.Components))
	}

	first := rep.Components[0]
	if first.Name != "Deposit IDR" || first.Group != "Fitur Transaksional" {
		t.Errorf("first component = %+v", first)
	}
	if first.Status != adapters.HealthOK {
		t.Errorf("first status = %q, want ok", first.Status)
	}
	if first.Position != 1 {
		t.Errorf("first position = %d, want 1", first.Position)
	}
	if !first.ChangedAt.IsZero() {
		t.Errorf("ChangedAt = %s, want the fixed timestamp ignored", first.ChangedAt)
	}

	staking := rep.Components[5]
	if staking.Name != "Staking" || staking.Status != adapters.HealthMaintenance {
		t.Errorf("Staking = %+v, want maintenance", staking)
	}

	last := rep.Components[17]
	if last.Name != "Chatroom" || last.Group != "Fitur Lainnya" {
		t.Errorf("last component = %+v", last)
	}
	if last.Position != 18 {
		t.Errorf("last position = %d, want 18", last.Position)
	}
	if !rep.SourceUpdatedAt.IsZero() {
		t.Errorf("SourceUpdatedAt = %s, want the fixed timestamp ignored", rep.SourceUpdatedAt)
	}
}

func TestIndodaxStatusVocabulary(t *testing.T) {
	cases := []struct {
		name  string
		class string
		want  adapters.Health
	}{
		{"green", "green", adapters.HealthOK},
		{"yellow", "yellow", adapters.HealthDegraded},
		{"red", "red", adapters.HealthMaintenance},
		{"unknown", "blue", adapters.HealthUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page := `<section class="status-section">
<h2 class="section-title">Fitur</h2>
<table class="idx-status-table"><tbody><tr>
<td><strong>Feature</strong></td>
<td><i class="status-line-` + tc.class + `"></i></td>
<td>2026-10-01 18:00 WIB</td>
</tr></tbody></table>
</section>`
			srv, adapter := serveIndodax(t, page)
			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if rep.Global != tc.want || rep.Components[0].Status != tc.want {
				t.Errorf("status = %q, global = %q, want %q", rep.Components[0].Status, rep.Global, tc.want)
			}
		})
	}
}

func TestIndodaxMissingStatusIsUnknown(t *testing.T) {
	page := `<section class="status-section">
<h2 class="section-title">Fitur</h2>
<table class="idx-status-table"><tbody><tr>
<td><strong>Feature</strong></td>
<td>No status icon</td>
<td>2026-10-01 18:00 WIB</td>
</tr></tbody></table>
</section>`
	srv, adapter := serveIndodax(t, page)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthUnknown || rep.Components[0].Status != adapters.HealthUnknown {
		t.Errorf("missing status was read as %q, want unknown", rep.Components[0].Status)
	}
}

func TestIndodaxIncludeFilter(t *testing.T) {
	srv, adapter := serveIndodax(t, realPage(t))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Include: []string{"  STAKING  "},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want only the one asked for", len(rep.Components))
	}
	if rep.Components[0].Name != "Staking" || rep.Components[0].Status != adapters.HealthMaintenance {
		t.Errorf("component = %+v", rep.Components[0])
	}
	if rep.Components[0].Position != 6 {
		t.Errorf("position = %d, want 6", rep.Components[0].Position)
	}
	if rep.Global != adapters.HealthMaintenance {
		t.Errorf("Global = %q, want the unfiltered page status", rep.Global)
	}
}

func TestIndodaxRejectsPageWithoutTable(t *testing.T) {
	srv, adapter := serveIndodax(t, `<html><body>not here</body></html>`)
	_, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a page with no status table")
	}
	if !strings.Contains(err.Error(), "lists no features") {
		t.Errorf("error = %q, want it to mention the missing features", err.Error())
	}
}

func TestIndodaxReturnsTheHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	rep, err := NewIndodax(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a refusing page")
	}
	if rep.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("HTTPStatus = %d, want 429", rep.HTTPStatus)
	}
}
