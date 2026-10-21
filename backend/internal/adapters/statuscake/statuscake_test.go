package statuscake

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"downradar.info/backend/internal/adapters"
)

const testPage = `<html><body>
<div class="notification is-warning" id="status-bar">
  <div class="level-left">Some systems are experiencing issues</div>
</div>
<section class="section">
  <div class="singleton-container">
    <div class="singleton-status singleton-parent level">
      <div class="level-left"><span>Admin</span></div>
      <div class="level-right"><span class="tag is-success">Good Service</span></div>
    </div>
    <div class="singleton-children">
      <div class="level singleton-with-parent is-hidden" data-id="p13298">
        <div class="level-left"><span>US1 Admin</span></div>
        <div class="level-right"><span class="tag is-success">Good Service</span></div>
      </div>
    </div>
  </div>
  <div class="singleton-container">
    <div class="singleton-status singleton-parent level">
      <div class="level-left"><span>API</span></div>
      <div class="level-right"><span class="tag is-danger">Service Outage</span></div>
    </div>
    <div class="singleton-children"></div>
  </div>
</section>
</body></html>`

func TestStatusCakeFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, testPage)
	}))
	defer srv.Close()

	rep, err := NewStatusCake(srv.Client()).Fetch(context.Background(), adapters.Target{
		Slug: "mailchimp", URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthMajor)
	}
	if len(rep.Components) != 2 {
		t.Fatalf("components = %d, want 2 (children are not separate services)", len(rep.Components))
	}
	cases := map[string]adapters.Health{
		"Admin": adapters.HealthOK,
		"API":   adapters.HealthMajor,
	}
	for _, c := range rep.Components {
		if want, ok := cases[c.Name]; ok && c.Status != want {
			t.Errorf("%s: status = %q, want %q", c.Name, c.Status, want)
		}
	}
}

func TestStatusCakeRefusesAnotherPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>not here</body></html>`)
	}))
	defer srv.Close()

	_, err := NewStatusCake(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch accepted a page with no status bar")
	}
}
