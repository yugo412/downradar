package store

import (
	"context"
	"testing"

	"downradar.info/backend/internal/adapters"
)

// A feed says where a change came from as well as where it landed. That is the
// status the target was last recorded in, which is what a page reading the same
// history is shown as well.
func TestRecentChangesCarriesTheStatusItMovedFrom(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	const slug = "zz-test-feed-changes"
	id := alertService(t, st, slug)

	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	changes, err := st.RecentChanges(ctx, 200)
	if err != nil {
		t.Fatalf("RecentChanges: %v", err)
	}

	var mine []ChangeView
	for _, change := range changes {
		if change.VendorSlug == slug {
			mine = append(mine, change)
		}
	}
	// The first status a service is seen in is where it started rather than a
	// change anybody was told about, so only the second poll is carried.
	if len(mine) != 1 {
		t.Fatalf("changes for %s = %d, want 1", slug, len(mine))
	}

	got := mine[0]
	if got.From != string(adapters.HealthOK) || got.To != string(adapters.HealthMajor) {
		t.Errorf("change = %s to %s, want ok to major", got.From, got.To)
	}
	if got.VendorName != slug {
		t.Errorf("vendor name = %q, want %q", got.VendorName, slug)
	}
	if got.ID == 0 {
		t.Error("change carries no event id")
	}
	if got.ChangedAt.IsZero() {
		t.Error("change carries no time")
	}

	// A vendor feed reads the vendor's own page, so the same history has to carry
	// the same thing there.
	detail, err := st.GetVendor(ctx, slug, Public())
	if err != nil {
		t.Fatalf("GetVendor: %v", err)
	}
	if len(detail.RecentEvents) == 0 {
		t.Fatal("the vendor page carries no events")
	}
	if newest := detail.RecentEvents[0]; newest.Previous != string(adapters.HealthOK) {
		t.Errorf("newest event moved from %q, want ok", newest.Previous)
	}
}

// A feed is public, so a service the catalogue does not show is not carried in it
// either.
func TestRecentChangesLeavesOutPrivateServices(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	const slug = "zz-test-feed-private"
	id := alertService(t, st, slug)
	if _, err := st.pool.Exec(ctx, `
		update services set owner_id = $2 where id = $1`, id, owner.ID); err != nil {
		t.Fatalf("make the service private: %v", err)
	}

	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthDegraded)); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	changes, err := st.RecentChanges(ctx, 200)
	if err != nil {
		t.Fatalf("RecentChanges: %v", err)
	}
	for _, change := range changes {
		if change.VendorSlug == slug {
			t.Fatalf("a private service was carried in the feed: %+v", change)
		}
	}
}
