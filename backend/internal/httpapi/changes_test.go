package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

// The changes endpoint is what a feed for the whole site is built from, so it is
// public, and it carries the vendor beside the change because a feed across the
// catalogue has no other way of saying whose change it was.
func TestChangesCarriesTheNewestChanges(t *testing.T) {
	st, router := testRouter(t)

	slug := alertVendor(t, st)
	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	// The first status a vendor is seen in is a baseline rather than a change, so
	// the second poll is what the feed is expected to carry.
	for _, status := range []adapters.Health{adapters.HealthOK, adapters.HealthMajor} {
		if _, err := st.ApplyReport(context.Background(), id, adapters.Report{
			Global:    status,
			FetchedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("ApplyReport: %v", err)
		}
	}

	rec := call(t, router, http.MethodGet, "/api/v1/changes?limit=200", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Changes []struct {
			VendorSlug string `json:"vendorSlug"`
			VendorName string `json:"vendorName"`
			From       string `json:"from"`
			To         string `json:"to"`
			ChangedAt  string `json:"changedAt"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	found := false
	for _, change := range body.Changes {
		if change.VendorSlug != slug {
			continue
		}
		found = true
		if change.From != string(adapters.HealthOK) || change.To != string(adapters.HealthMajor) {
			t.Errorf("change = %s to %s, want ok to major", change.From, change.To)
		}
		if change.VendorName == "" {
			t.Error("change carries no vendor name")
		}
		if change.ChangedAt == "" {
			t.Error("change carries no time")
		}
	}
	if !found {
		t.Fatalf("%s is not in the changes", slug)
	}
}

// A limit nobody can read is refused rather than quietly replaced with the
// default, so a feed asking for something odd is not handed something else.
func TestChangesRefusesALimitItCannotRead(t *testing.T) {
	_, router := testRouter(t)

	for _, path := range []string{"/api/v1/changes?limit=soon", "/api/v1/changes?limit=0"} {
		rec := call(t, router, http.MethodGet, path, "", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400: %s", path, rec.Code, rec.Body.String())
		}
	}
}
