package store

import (
	"context"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

// A hidden component keeps its row and its state but leaves the public page, the
// vendor counts and the change list. Showing it again brings all of it back.
func TestHiddenComponentLeavesThePublicPage(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	slug := uniqueName("hidden-component")
	id := testService(t, st, slug)

	report := func(web, noise adapters.Health) adapters.Report {
		return adapters.Report{
			Global:    adapters.HealthOK,
			FetchedAt: time.Now().UTC(),
			Components: []adapters.ComponentStatus{
				{SourceID: "web", Name: "Web", Position: 1, Status: web},
				{SourceID: "noise", Name: "Visit the page for more information", Position: 2, Status: noise},
			},
		}
	}
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthOK, adapters.HealthOK)); err != nil {
		t.Fatalf("ApplyReport: %v", err)
	}
	// A real change for the noise component gives the public change list and the
	// vendor's recent events something that hiding has to remove.
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthOK, adapters.HealthDegraded)); err != nil {
		t.Fatalf("second ApplyReport: %v", err)
	}

	detail, err := st.GetVendor(ctx, slug, Public())
	if err != nil {
		t.Fatalf("GetVendor: %v", err)
	}
	if len(detail.ComponentsList) != 2 {
		t.Fatalf("components = %d, want both before hiding", len(detail.ComponentsList))
	}

	if err := st.SetComponentHidden(ctx, slug, "noise", true); err != nil {
		t.Fatalf("SetComponentHidden: %v", err)
	}

	detail, err = st.GetVendor(ctx, slug, Public())
	if err != nil {
		t.Fatalf("GetVendor after hiding: %v", err)
	}
	if detail.Components != 1 || len(detail.ComponentsList) != 1 {
		t.Errorf("components = %d/%d, want the hidden one left out", detail.Components, len(detail.ComponentsList))
	}
	if detail.ComponentsList[0].SourceID != "web" {
		t.Errorf("the public list kept %q, want web", detail.ComponentsList[0].SourceID)
	}
	for _, event := range detail.RecentEvents {
		if event.Component == "Visit the page for more information" {
			t.Error("a hidden component still has events on the public page")
		}
	}

	changes, err := st.RecentChanges(ctx, 100)
	if err != nil {
		t.Fatalf("RecentChanges: %v", err)
	}
	for _, change := range changes {
		if change.VendorSlug == slug && change.Component == "Visit the page for more information" {
			t.Error("a hidden component still has changes in the public feed")
		}
	}

	all, err := st.ServiceComponents(ctx, slug)
	if err != nil {
		t.Fatalf("ServiceComponents: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("staff components = %d, want the hidden one too", len(all))
	}
	var hidden *ComponentView
	for i := range all {
		if all[i].SourceID == "noise" {
			hidden = &all[i]
		}
	}
	if hidden == nil || !hidden.Hidden {
		t.Errorf("hidden component = %+v, want it marked hidden", hidden)
	}

	vendors, err := st.ListVendors(ctx)
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	for _, vendor := range vendors {
		if vendor.Slug == slug && vendor.Components != 1 {
			t.Errorf("listed components = %d, want the hidden one left out", vendor.Components)
		}
	}

	if err := st.SetComponentHidden(ctx, slug, "noise", false); err != nil {
		t.Fatalf("show the component again: %v", err)
	}
	detail, err = st.GetVendor(ctx, slug, Public())
	if err != nil {
		t.Fatalf("GetVendor after showing: %v", err)
	}
	if len(detail.ComponentsList) != 2 {
		t.Errorf("components = %d after showing, want both", len(detail.ComponentsList))
	}
}

func TestHidingAComponentNeedsOneThatExists(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	slug := uniqueName("hidden-missing")
	testService(t, st, slug)

	if err := st.SetComponentHidden(ctx, slug, "missing", true); err == nil {
		t.Fatal("hiding a component that does not exist was accepted")
	}
	if err := st.SetComponentHidden(ctx, uniqueName("no-such-service"), "web", true); err == nil {
		t.Fatal("hiding a component on a service that does not exist was accepted")
	}
}
