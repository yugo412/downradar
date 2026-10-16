package store

import (
	"context"
	"testing"

	"downradar.info/backend/internal/adapters"
)

func TestSeedServicesWritesEveryColumn(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	target := adapters.Target{
		Slug:                  "zz-test-seed",
		Name:                  "Seed Test",
		URL:                   "https://status.example.com",
		Category:              "testing",
		Profile:               "statuspage-v2",
		Kind:                  "statuspage",
		Endpoint:              "/api/v2/summary.json",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
		AllowSharedPage:       true,
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(), `delete from services where slug = $1`, target.Slug)
	})

	if _, err := st.SeedServices(ctx, []adapters.Target{target}); err != nil {
		t.Fatalf("SeedServices: %v", err)
	}

	var (
		profile    string
		endpoint   string
		include    []string
		aliases    []string
		shared     bool
		category   string
		includeNil bool
	)
	err := st.pool.QueryRow(ctx, `
		select coalesce(profile, ''), coalesce(endpoint, ''), include_components, aliases,
		       allow_shared_page, coalesce(category, ''), include_components is null
		from services where slug = $1`, target.Slug,
	).Scan(&profile, &endpoint, &include, &aliases, &shared, &category, &includeNil)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if profile != "statuspage-v2" || endpoint != "/api/v2/summary.json" {
		t.Errorf("profile/endpoint = %q/%q", profile, endpoint)
	}
	if category != "testing" || !shared {
		t.Errorf("category/shared = %q/%v", category, shared)
	}
	// Empty arrays must be stored as empty, not NULL: the columns are NOT NULL.
	if includeNil || include == nil || aliases == nil {
		t.Errorf("array columns should be empty arrays, got include=%v aliases=%v", include, aliases)
	}
}

func TestSeedServicesKeepsVendorsItWasNotGiven(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	existing := testService(t, st, "zz-test-seed-survivor")

	// A seed file that does not mention the existing vendor must not deactivate
	// it: a row added from a form has to survive a reseed.
	if _, err := st.SeedServices(ctx, []adapters.Target{{
		Slug:                  "zz-test-seed-other",
		Name:                  "Other",
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	}}); err != nil {
		t.Fatalf("SeedServices: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(), `delete from services where slug = $1`, "zz-test-seed-other")
	})

	var active bool
	if err := st.pool.QueryRow(ctx,
		`select active from services where id = $1`, existing).Scan(&active); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !active {
		t.Error("seeding must not deactivate vendors it was not given")
	}
}

func TestServicesReturnsRowsForThePoller(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := testService(t, st, "zz-test-services-read")
	if _, err := st.pool.Exec(ctx, `
		update services set profile = 'statuspage-v2', endpoint = '/api/v2/summary.json',
		       aliases = array['a','b']
		where id = $1`, id); err != nil {
		t.Fatalf("prepare row: %v", err)
	}

	rows, err := st.Services(ctx)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}

	var found *ServiceRow
	for i := range rows {
		if rows[i].ID == id {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("the vendor was not returned")
	}
	if found.Profile != "statuspage-v2" || found.Endpoint != "/api/v2/summary.json" {
		t.Errorf("profile/endpoint = %q/%q", found.Profile, found.Endpoint)
	}
	if len(found.Aliases) != 2 {
		t.Errorf("aliases = %v, want two entries", found.Aliases)
	}
	if found.Status != adapters.HealthUnknown {
		t.Errorf("status = %q, want unknown before the first poll", found.Status)
	}
}

func TestProposeServiceIsNotPolledUntilPublished(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug, err := st.ProposeService(ctx, "zz-test Proposed Vendor", "https://status.example.com")
	if err != nil {
		t.Fatalf("ProposeService: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(), `delete from services where slug = $1`, slug)
	})

	if slug != "zz-test-proposed-vendor" {
		t.Errorf("slug = %q, want it derived from the name", slug)
	}

	var status string
	if err := st.pool.QueryRow(ctx, `select status from services where slug = $1`, slug).Scan(&status); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != string(StatusProposed) {
		t.Errorf("status = %q, want proposed", status)
	}

	// The poller must not see it, and neither must the public list.
	rows, err := st.Services(ctx)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	for _, r := range rows {
		if r.Slug == slug {
			t.Error("a proposed vendor must not be polled")
		}
	}

	vendors, err := st.ListVendors(ctx)
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	for _, v := range vendors {
		if v.Slug == slug {
			t.Error("a proposed vendor must not appear on the site")
		}
	}

	if _, err := st.GetVendor(ctx, slug, Public()); err == nil {
		t.Error("a proposed vendor must not have a public page")
	}
}

func TestProposeServiceMakesTheSlugUnique(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	first, err := st.ProposeService(ctx, "zz-test Duplicate Name", "")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := st.ProposeService(ctx, "zz-test Duplicate Name", "")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(),
			`delete from services where slug = any($1)`, []string{first, second})
	})

	if first == second {
		t.Errorf("both proposals got the slug %q", first)
	}
	if second != first+"-2" {
		t.Errorf("second slug = %q, want %q", second, first+"-2")
	}
}

func TestProposeServiceAcceptsAMissingURL(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug, err := st.ProposeService(ctx, "zz-test No URL", "")
	if err != nil {
		t.Fatalf("ProposeService: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(), `delete from services where slug = $1`, slug)
	})

	var url *string
	if err := st.pool.QueryRow(ctx, `select url from services where slug = $1`, slug).Scan(&url); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if url != nil {
		t.Errorf("url = %q, want NULL when the form left it empty", *url)
	}
}

func TestSeedServicesPublishesAProposedRow(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug, err := st.ProposeService(ctx, "zz-test Seeded Later", "https://status.example.com")
	if err != nil {
		t.Fatalf("ProposeService: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(), `delete from services where slug = $1`, slug)
	})

	// The seed file is the authoritative list, so a name that shows up there is
	// live even if it arrived as a proposal first.
	if _, err := st.SeedServices(ctx, []adapters.Target{{
		Slug:                  slug,
		Name:                  "Seeded Later",
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		Kind:                  "statuspage",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	}}); err != nil {
		t.Fatalf("SeedServices: %v", err)
	}

	var status string
	if err := st.pool.QueryRow(ctx, `select status from services where slug = $1`, slug).Scan(&status); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != string(StatusPublished) {
		t.Errorf("status = %q, want published", status)
	}

	rows, err := st.Services(ctx)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.Slug == slug {
			found = true
		}
	}
	if !found {
		t.Error("a seeded vendor must be polled")
	}
}
