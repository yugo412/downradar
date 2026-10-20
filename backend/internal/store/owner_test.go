package store

import (
	"context"
	"errors"
	"testing"
)

func newTestUser(t *testing.T, st *Store, role string) *User {
	t.Helper()

	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.CreateUser(context.Background(), email, "Owner", role, "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}

func newPrivateService(t *testing.T, st *Store, owner *User) string {
	t.Helper()

	name := uniqueName("private")
	slug, err := st.CreateService(context.Background(), NewService{
		Name:                  name,
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
		OwnerID:               &owner.ID,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })
	return slug
}

// The point of the whole feature: a private service is polled, but it must not
// show up anywhere the public can reach.
func TestPrivateServiceStaysOutOfEverythingPublic(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	stranger := newTestUser(t, st, "admin")

	before, err := st.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	slug := newPrivateService(t, st, owner)

	t.Run("the public list leaves it out", func(t *testing.T) {
		vendors, err := st.ListVendors(ctx)
		if err != nil {
			t.Fatalf("ListVendors: %v", err)
		}
		for _, v := range vendors {
			if v.Slug == slug {
				t.Fatal("a private service was listed publicly")
			}
		}
	})

	t.Run("the public stats leave it out", func(t *testing.T) {
		after, err := st.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if after.Total != before.Total {
			t.Errorf("total went from %d to %d, so a private service was counted", before.Total, after.Total)
		}
	})

	t.Run("the public detail endpoint refuses it", func(t *testing.T) {
		if _, err := st.GetVendor(ctx, slug, Public()); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound for the public", err)
		}
	})

	t.Run("another signed in user cannot read it", func(t *testing.T) {
		viewer := Viewer{UserID: stranger.ID, IsStaff: true}
		if _, err := st.GetVendor(ctx, slug, viewer); err != nil {
			t.Errorf("staff should be able to read it: %v", err)
		}
		viewer = Viewer{UserID: stranger.ID}
		if _, err := st.GetVendor(ctx, slug, viewer); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound for somebody else", err)
		}
	})

	t.Run("the owner can read it", func(t *testing.T) {
		if _, err := st.GetVendor(ctx, slug, Viewer{UserID: owner.ID}); err != nil {
			t.Errorf("the owner cannot read their own service: %v", err)
		}
	})

	t.Run("it is still polled", func(t *testing.T) {
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
			t.Error("a private service must still be polled")
		}
	})

	t.Run("it does not block a public suggestion of the same name", func(t *testing.T) {
		service, err := st.AdminGetService(ctx, slug)
		if err != nil {
			t.Fatalf("AdminGetService: %v", err)
		}
		found, err := st.FindVendor(ctx, service.Name)
		if err != nil {
			t.Fatalf("FindVendor: %v", err)
		}
		if found != "" {
			t.Errorf("FindVendor returned %q, so a private service blocks the public form", found)
		}
	})
}

func TestServiceListShowsOnlyWhatTheViewerMaySee(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	other := newTestUser(t, st, "subscriber")

	mine := newPrivateService(t, st, owner)
	theirs := newPrivateService(t, st, other)

	t.Run("a subscriber sees only their own", func(t *testing.T) {
		list, err := st.AdminListServices(ctx, "all", "", Viewer{UserID: owner.ID})
		if err != nil {
			t.Fatalf("AdminListServices: %v", err)
		}
		if !containsSlug(list, mine) {
			t.Error("the owner cannot see their own private service")
		}
		if containsSlug(list, theirs) {
			t.Error("a subscriber saw somebody else's private service")
		}
		for _, row := range list {
			if row.OwnerID == nil || *row.OwnerID != owner.ID {
				t.Errorf("%s is in a subscriber's list but is not theirs", row.Slug)
			}
		}
	})

	t.Run("staff see every row", func(t *testing.T) {
		list, err := st.AdminListServices(ctx, "all", "", staffViewer())
		if err != nil {
			t.Fatalf("AdminListServices: %v", err)
		}
		if !containsSlug(list, mine) || !containsSlug(list, theirs) {
			t.Error("staff should see both private services")
		}
	})

	t.Run("the owner is named on the row", func(t *testing.T) {
		list, err := st.AdminListServices(ctx, "all", "", staffViewer())
		if err != nil {
			t.Fatalf("AdminListServices: %v", err)
		}
		for _, row := range list {
			if row.Slug != mine {
				continue
			}
			if row.OwnerID == nil || *row.OwnerID != owner.ID {
				t.Errorf("owner id = %v, want %d", row.OwnerID, owner.ID)
			}
			if row.OwnerName != "Owner" || row.OwnerEmail != owner.Email {
				t.Errorf("owner = %q / %q, want the account that owns it", row.OwnerName, row.OwnerEmail)
			}
		}
	})
}

func TestMovingAServiceBetweenTheCatalogueAndPrivate(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "admin")

	name := uniqueName("toggled")
	slug, err := st.CreateService(ctx, NewService{
		Name:                  name,
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	// It starts public.
	if _, err := st.GetVendor(ctx, slug, Public()); err != nil {
		t.Fatalf("a new service should be public: %v", err)
	}

	// Take it private, the way the staff screen does.
	private := VisibilityPrivate
	if err := st.UpdateService(ctx, slug, ServicePatch{
		SetOwner: true, OwnerID: &owner.ID, Visibility: &private,
	}); err != nil {
		t.Fatalf("UpdateService to private: %v", err)
	}
	if _, err := st.GetVendor(ctx, slug, Public()); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want it gone from the public after going private", err)
	}
	if _, err := st.GetVendor(ctx, slug, Viewer{UserID: owner.ID}); err != nil {
		t.Errorf("the owner cannot read it after going private: %v", err)
	}

	// And back to the catalogue. It keeps its owner: who may see a row and who
	// looks after it are separate answers.
	public := VisibilityPublic
	if err := st.UpdateService(ctx, slug, ServicePatch{Visibility: &public}); err != nil {
		t.Fatalf("UpdateService to public: %v", err)
	}
	if _, err := st.GetVendor(ctx, slug, Public()); err != nil {
		t.Errorf("the service did not come back to the catalogue: %v", err)
	}
	row, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if row.OwnerID == nil || *row.OwnerID != owner.ID {
		t.Errorf("owner = %v, want it left with %d", row.OwnerID, owner.ID)
	}
}

// A service a subscriber published is in the catalogue for everybody and still
// theirs: it is on their list, and their name is on the row staff read.
func TestAPublicServiceCanBelongToSomebody(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	other := newTestUser(t, st, "subscriber")

	name := uniqueName("published by a subscriber")
	slug, err := st.CreateService(ctx, NewService{
		Name:                  name,
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
		Visibility:            VisibilityPublic,
		OwnerID:               &owner.ID,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	if _, err := st.GetVendor(ctx, slug, Public()); err != nil {
		t.Errorf("the catalogue cannot read a published service: %v", err)
	}

	mine, err := st.OwnedVendors(ctx, owner.ID)
	if err != nil {
		t.Fatalf("OwnedVendors: %v", err)
	}
	var found bool
	for _, vendor := range mine {
		if vendor.Slug == slug {
			found = true
		}
	}
	if !found {
		t.Error("the service is missing from the list of the person who added it")
	}

	theirs, err := st.AdminListServices(ctx, "all", "", Viewer{UserID: other.ID})
	if err != nil {
		t.Fatalf("AdminListServices: %v", err)
	}
	for _, row := range theirs {
		if row.Slug == slug {
			t.Error("another subscriber can see a row that is not theirs")
		}
	}
}

func TestOwnedVendorsIsTheDashboardList(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	other := newTestUser(t, st, "subscriber")

	mine := newPrivateService(t, st, owner)
	theirs := newPrivateService(t, st, other)

	owned, err := st.OwnedVendors(ctx, owner.ID)
	if err != nil {
		t.Fatalf("OwnedVendors: %v", err)
	}

	found := false
	for _, v := range owned {
		if v.Slug == theirs {
			t.Error("somebody else's private service was handed to the dashboard")
		}
		if v.Slug == mine {
			found = true
			if v.Status == "" {
				t.Error("the row carries no status, so the card cannot show one")
			}
		}
	}
	if !found {
		t.Error("the owner's own private service is missing from the dashboard list")
	}

	// Staff may see everything in the management list, but the dashboard is
	// about what you own, so somebody who owns nothing gets nothing.
	staff := newTestUser(t, st, "admin")
	staffOwned, err := st.OwnedVendors(ctx, staff.ID)
	if err != nil {
		t.Fatalf("OwnedVendors: %v", err)
	}
	if len(staffOwned) != 0 {
		t.Errorf("a staff dashboard returned %d rows, want only what they own", len(staffOwned))
	}
}
