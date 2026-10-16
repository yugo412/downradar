package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func uniqueName(prefix string) string {
	return fmt.Sprintf("zz-test %s %d", prefix, time.Now().UnixNano())
}

func dropServices(ctx context.Context, st *Store, slugs ...string) {
	for _, slug := range slugs {
		_, _ = st.pool.Exec(ctx, `delete from services where slug = $1`, slug)
	}
}

func TestCreateServiceIsPublishedAndPollable(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	name := uniqueName("created")
	slug, err := st.CreateService(ctx, NewService{
		Name:                  name,
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		Category:              "testing",
		Aliases:               []string{"zz test"},
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	created, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if created.Status != string(StatusPublished) {
		t.Errorf("status = %q, want published", created.Status)
	}
	if !created.Active {
		t.Error("a new service should be active")
	}
	if created.Profile != "statuspage-v2" || created.PollIntervalSeconds != 300 {
		t.Errorf("profile/interval = %q/%d", created.Profile, created.PollIntervalSeconds)
	}
	if len(created.Aliases) != 1 {
		t.Errorf("aliases = %v", created.Aliases)
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
		t.Error("a service added from the form must be polled straight away")
	}
}

func TestAdminListServicesFilters(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	proposed, err := st.ProposeService(ctx, uniqueName("proposed"), "https://status.example.com")
	if err != nil {
		t.Fatalf("ProposeService: %v", err)
	}
	published, err := st.CreateService(ctx, NewService{
		Name: uniqueName("published"), URL: "https://status.example.com",
		Profile: "statuspage-v2", PollIntervalSeconds: 300, ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, proposed, published) })

	proposedList, err := st.AdminListServices(ctx, "proposed", "", staffViewer())
	if err != nil {
		t.Fatalf("AdminListServices: %v", err)
	}
	if !containsSlug(proposedList, proposed) {
		t.Error("the proposal is missing from the proposed filter")
	}
	if containsSlug(proposedList, published) {
		t.Error("a published service showed up under the proposed filter")
	}

	publishedList, err := st.AdminListServices(ctx, "published", "", staffViewer())
	if err != nil {
		t.Fatalf("AdminListServices: %v", err)
	}
	if !containsSlug(publishedList, published) || containsSlug(publishedList, proposed) {
		t.Error("the published filter returned the wrong rows")
	}

	all, err := st.AdminListServices(ctx, "all", "", staffViewer())
	if err != nil {
		t.Fatalf("AdminListServices: %v", err)
	}
	if !containsSlug(all, proposed) || !containsSlug(all, published) {
		t.Error("the all filter is missing a row")
	}
}

func TestUpdateServicePatchSemantics(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug, err := st.CreateService(ctx, NewService{
		Name: uniqueName("patched"), URL: "https://status.example.com",
		Profile: "statuspage-v2", Category: "testing",
		PollIntervalSeconds: 300, ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	newName := "Renamed Service"
	newCategory := ""
	if err := st.UpdateService(ctx, slug, ServicePatch{Name: &newName, Category: &newCategory}); err != nil {
		t.Fatalf("UpdateService: %v", err)
	}

	updated, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if updated.Name != newName {
		t.Errorf("name = %q, want %q", updated.Name, newName)
	}
	if updated.Category != "" {
		t.Errorf("category = %q, want it cleared", updated.Category)
	}
	// Fields that were not in the patch must survive.
	if updated.URL != "https://status.example.com" || updated.Profile != "statuspage-v2" {
		t.Errorf("an untouched field changed: url=%q profile=%q", updated.URL, updated.Profile)
	}

	if err := st.UpdateService(ctx, slug, ServicePatch{Name: &newName}); err != nil {
		t.Fatalf("second UpdateService: %v", err)
	}
	if err := st.UpdateService(ctx, "zz-test-does-not-exist", ServicePatch{Name: &newName}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteProposalRemovesTheRow(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug, err := st.ProposeService(ctx, uniqueName("rejected"), "")
	if err != nil {
		t.Fatalf("ProposeService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	outcome, err := st.DeleteService(ctx, slug)
	if err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	if outcome != DeletePermanently {
		t.Errorf("outcome = %q, want %q", outcome, DeletePermanently)
	}
	if _, err := st.AdminGetService(ctx, slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want the proposal to be gone", err)
	}
}

func TestDeletePublishedVendorKeepsItForRestore(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug, err := st.CreateService(ctx, NewService{
		Name: uniqueName("removed"), URL: "https://status.example.com",
		Profile: "statuspage-v2", PollIntervalSeconds: 300, ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	outcome, err := st.DeleteService(ctx, slug)
	if err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	if outcome != DeleteDeactivated {
		t.Errorf("outcome = %q, want %q", outcome, DeleteDeactivated)
	}

	removed, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if removed.Active {
		t.Error("a removed service should not be active")
	}

	// Gone from the site and the poller, but still in the table.
	vendors, err := st.ListVendors(ctx)
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	for _, v := range vendors {
		if v.Slug == slug {
			t.Error("a removed service must not be listed publicly")
		}
	}
	rows, err := st.Services(ctx)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	for _, r := range rows {
		if r.Slug == slug {
			t.Error("a removed service must not be polled")
		}
	}

	removedList, err := st.AdminListServices(ctx, "removed", "", staffViewer())
	if err != nil {
		t.Fatalf("AdminListServices: %v", err)
	}
	if !containsSlug(removedList, slug) {
		t.Error("a removed service should be visible under the removed filter")
	}

	restore := true
	if err := st.UpdateService(ctx, slug, ServicePatch{Active: &restore}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	back, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if !back.Active {
		t.Error("the service was not restored")
	}
}

func TestDeleteServiceForAnUnknownSlug(t *testing.T) {
	st := integrationStore(t)

	if _, err := st.DeleteService(context.Background(), "zz-test-does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func staffViewer() Viewer {
	return Viewer{IsStaff: true}
}

func containsSlug(list []AdminService, slug string) bool {
	for _, a := range list {
		if a.Slug == slug {
			return true
		}
	}
	return false
}

// Every row this checks is made here. The test database starts empty, so a test
// must never assume the development seed has been loaded.
func TestDistinctCategories(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	withCategory := func(category string) {
		t.Helper()

		slug, err := st.CreateService(ctx, NewService{
			Name:                  uniqueName("category"),
			URL:                   "https://status.example.com",
			Profile:               "statuspage-v2",
			Category:              category,
			PollIntervalSeconds:   300,
			ActiveIntervalSeconds: 60,
		})
		if err != nil {
			t.Fatalf("CreateService: %v", err)
		}
		t.Cleanup(func() { dropServices(context.Background(), st, slug) })
	}

	withCategory("zz-alpha")
	withCategory("zz-alpha") // twice on purpose: it should be offered once
	withCategory("zz-bravo")
	withCategory("") // no category at all, which is not a suggestion

	categories, err := st.DistinctCategories(ctx)
	if err != nil {
		t.Fatalf("DistinctCategories: %v", err)
	}

	seen := make(map[string]bool, len(categories))
	for i, category := range categories {
		if category == "" {
			t.Error("an empty category was returned")
		}
		if seen[category] {
			t.Errorf("category %q was returned twice", category)
		}
		seen[category] = true
		if i > 0 && categories[i-1] > category {
			t.Errorf("not sorted: %q came before %q", categories[i-1], category)
		}
	}

	if !seen["zz-alpha"] || !seen["zz-bravo"] {
		t.Errorf("the categories in use are missing from %v", categories)
	}
	if seen["zz-never-used"] {
		t.Error("a category nobody uses was returned")
	}
}

func TestAdminListServicesSearch(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	name := uniqueName("searchable")
	slug, err := st.CreateService(ctx, NewService{
		Name:                  name,
		URL:                   "https://zz-search.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	cases := []struct {
		label  string
		search string
	}{
		{"by name", name},
		{"by name, ignoring case", strings.ToUpper(name)},
		{"by a fragment of the name", "searchable"},
		{"by slug", slug},
		{"by url", "zz-search.example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got, err := st.AdminListServices(ctx, "all", tc.search, staffViewer())
			if err != nil {
				t.Fatalf("AdminListServices: %v", err)
			}
			if !containsSlug(got, slug) {
				t.Errorf("searching %q did not find the service", tc.search)
			}
		})
	}

	t.Run("a term that matches nothing", func(t *testing.T) {
		got, err := st.AdminListServices(ctx, "all", "zz-nothing-matches-this", staffViewer())
		if err != nil {
			t.Fatalf("AdminListServices: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d rows, want none", len(got))
		}
	})

	t.Run("a percent sign is a literal, not a wildcard", func(t *testing.T) {
		all, err := st.AdminListServices(ctx, "all", "", staffViewer())
		if err != nil {
			t.Fatalf("AdminListServices: %v", err)
		}
		wildcard, err := st.AdminListServices(ctx, "all", "%", staffViewer())
		if err != nil {
			t.Fatalf("AdminListServices: %v", err)
		}
		if len(wildcard) >= len(all) {
			t.Errorf("searching %% returned %d of %d rows, so it acted as a wildcard", len(wildcard), len(all))
		}
	})

	t.Run("an empty search keeps every row", func(t *testing.T) {
		got, err := st.AdminListServices(ctx, "all", "", staffViewer())
		if err != nil {
			t.Fatalf("AdminListServices: %v", err)
		}
		if !containsSlug(got, slug) {
			t.Error("an empty search dropped a row")
		}
	})

	t.Run("search still respects the filter", func(t *testing.T) {
		got, err := st.AdminListServices(ctx, "proposed", name, staffViewer())
		if err != nil {
			t.Fatalf("AdminListServices: %v", err)
		}
		if containsSlug(got, slug) {
			t.Error("a published service was returned under the proposed filter")
		}
	})
}
