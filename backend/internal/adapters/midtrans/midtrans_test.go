package midtrans

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"downradar.info/backend/internal/adapters"
)

const testPage = `<html><body>
<div class="status-wrapper">
  <div class="status-head clearfix">
    <h2>All Systems Operational</h2>
    <span class="s-h2-sub">Updated 3 days ago</span>
  </div>
  <div class="status-main">
    <div class="status-list">
      <h3>SNAP</h3>
      <span class="status-badge">Primary Data Center</span>
      <div class="status-opt">
        Operational
      </div>
    </div>
    <div class="status-list">
      <h3>Core API</h3>
      <span class="status-badge">Primary Data Center</span>
      <div class="status-opt">
        Service Outage
      </div>
    </div>
  </div>
</div>
</body></html>`

func TestMidtransFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, testPage)
	}))
	defer srv.Close()

	rep, err := NewMidtrans(srv.Client()).Fetch(context.Background(), adapters.Target{
		Slug: "midtrans", URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want the worst service even when the page says fine", rep.Global)
	}
	if len(rep.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(rep.Components))
	}
	cases := map[string]adapters.Health{
		"SNAP":     adapters.HealthOK,
		"Core API": adapters.HealthMajor,
	}
	for _, c := range rep.Components {
		if want, ok := cases[c.Name]; ok && c.Status != want {
			t.Errorf("%s: status = %q, want %q", c.Name, c.Status, want)
		}
	}
	if rep.Components[0].Group != "Primary Data Center" {
		t.Errorf("group = %q, want the data centre", rep.Components[0].Group)
	}
}

func TestMidtransRefusesAnotherPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>not here</body></html>`)
	}))
	defer srv.Close()

	_, err := NewMidtrans(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a page with no status section")
	}
}

func TestMidtransReadsTheOverallLine(t *testing.T) {
	page := `<html><body>
<div class="status-head clearfix"><h2>All Systems Operational</h2></div>
<div class="status-main">
  <div class="status-list">
    <h3>SNAP</h3>
    <div class="status-opt">Operational</div>
  </div>
</div>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer srv.Close()

	rep, err := NewMidtrans(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthOK)
	}
}
