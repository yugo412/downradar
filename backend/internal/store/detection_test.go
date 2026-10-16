package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// parkedService writes a row the way the add form leaves one when no profile
// matched: live, but with no adapter to poll it.
func parkedService(t *testing.T, st *Store, slug string) int64 {
	t.Helper()

	var id int64
	err := st.pool.QueryRow(context.Background(), `
		insert into services (slug, name, url, status, active)
		values ($1, $1, 'https://status.example.com', $2, true)
		returning id`, slug, string(StatusNoMatch)).Scan(&id)
	if err != nil {
		t.Fatalf("insert parked service: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })
	return id
}

func pendingSlugs(t *testing.T, st *Store) map[string]bool {
	t.Helper()

	rows, err := st.PendingDetection(context.Background())
	if err != nil {
		t.Fatalf("PendingDetection: %v", err)
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.Slug] = true
	}
	return out
}

// A vendor added without a profile that nothing could read has to keep that
// outcome: the row is what the poller looks for later.
func TestCreateServiceKeepsTheStatusItIsGiven(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug, err := st.CreateService(ctx, NewService{
		Name: uniqueName("parked create"), URL: "https://status.example.com",
		Status: string(StatusNoMatch),
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	row, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if row.Status != string(StatusNoMatch) {
		t.Errorf("status = %q, want no_match", row.Status)
	}
	if row.Profile != "" {
		t.Errorf("profile = %q, want it left empty", row.Profile)
	}
	if row.PollIntervalSeconds != defaultPollIntervalSeconds ||
		row.ActiveIntervalSeconds != defaultActiveIntervalSeconds {
		t.Errorf("intervals = %d/%d, want the defaults %d/%d",
			row.PollIntervalSeconds, row.ActiveIntervalSeconds,
			defaultPollIntervalSeconds, defaultActiveIntervalSeconds)
	}
}

func TestPendingDetectionFindsOnlyTheVendorsWithoutAProfile(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	parked := fmt.Sprintf("zz-test-parked-%d", time.Now().UnixNano())
	parkedService(t, st, parked)

	live, err := st.CreateService(ctx, NewService{
		Name: uniqueName("live"), URL: "https://status.example.com",
		Profile: "statuspage-v2", PollIntervalSeconds: 300, ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, live) })

	proposal, err := st.ProposeService(ctx, uniqueName("proposal"), "https://status.example.com")
	if err != nil {
		t.Fatalf("ProposeService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, proposal) })

	pending := pendingSlugs(t, st)
	if !pending[parked] {
		t.Error("a vendor with no profile should be offered to the queue")
	}
	if pending[live] {
		t.Error("a vendor that is already polled should not be offered to the queue")
	}
	if pending[proposal] {
		t.Error("a proposal has not been accepted yet, so it must not be detected")
	}
}

func TestSetDetectedProfilePublishesTheVendor(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug := fmt.Sprintf("zz-test-detected-%d", time.Now().UnixNano())
	id := parkedService(t, st, slug)

	if err := st.SetDetectedProfile(ctx, id, "statusio", 120, 30); err != nil {
		t.Fatalf("SetDetectedProfile: %v", err)
	}

	row, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if row.Status != string(StatusPublished) {
		t.Errorf("status = %q, want published", row.Status)
	}
	if row.Profile != "statusio" {
		t.Errorf("profile = %q, want statusio", row.Profile)
	}
	if row.PollIntervalSeconds != 120 || row.ActiveIntervalSeconds != 30 {
		t.Errorf("intervals = %d/%d, want the detected profile's 120/30",
			row.PollIntervalSeconds, row.ActiveIntervalSeconds)
	}

	if pending := pendingSlugs(t, st); pending[slug] {
		t.Error("the vendor is still waiting to be detected")
	}

	polled, err := st.Services(ctx)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	found := false
	for _, r := range polled {
		if r.Slug == slug {
			found = true
		}
	}
	if !found {
		t.Error("a vendor that was just detected has to be polled from now on")
	}
}

func TestParkDetectionParksTheVendorAndRecordsTheAttempt(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug := fmt.Sprintf("zz-test-park-%d", time.Now().UnixNano())
	id := parkedService(t, st, slug)

	before, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if before.LastPolledAt != nil {
		t.Fatalf("last_polled_at = %v, want it unset before the first attempt", before.LastPolledAt)
	}

	reason := "no profile matched https://status.example.com (statuspage-v2: no page)"
	if err := st.ParkDetection(ctx, id, reason); err != nil {
		t.Fatalf("ParkDetection: %v", err)
	}

	after, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if after.LastPolledAt == nil {
		t.Error("the retry clock should have moved")
	}
	if after.Status != string(StatusNoMatch) {
		t.Errorf("status = %q, parking must leave the vendor waiting", after.Status)
	}

	runs, err := st.RecentPollRuns(ctx, slug, 10)
	if err != nil {
		t.Fatalf("RecentPollRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("poll runs = %d, want the attempt on record", len(runs))
	}
	if runs[0].OK {
		t.Error("an attempt that matched nothing is not a successful poll")
	}
	if runs[0].Error != reason {
		t.Errorf("poll run error = %q, want the reason the queue gave", runs[0].Error)
	}

	parked, err := st.Services(ctx)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	for _, row := range parked {
		if row.Slug == slug {
			t.Error("a parked vendor must not be in the poll rotation")
		}
	}
}

func TestAdminListServicesHasANoMatchFilter(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug := fmt.Sprintf("zz-test-filtered-%d", time.Now().UnixNano())
	parkedService(t, st, slug)

	parked, err := st.AdminListServices(ctx, string(StatusNoMatch), "", staffViewer())
	if err != nil {
		t.Fatalf("AdminListServices: %v", err)
	}
	if !containsSlug(parked, slug) {
		t.Error("the parked vendor is missing from the no_match filter")
	}

	published, err := st.AdminListServices(ctx, string(StatusPublished), "", staffViewer())
	if err != nil {
		t.Fatalf("AdminListServices: %v", err)
	}
	if containsSlug(published, slug) {
		t.Error("a parked vendor showed up under the published filter")
	}
}

func TestUpdateServiceRefreshesTheIntervalsWithTheProfile(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug, err := st.CreateService(ctx, NewService{
		Name: uniqueName("intervals"), URL: "https://status.example.com",
		Profile: "statuspage-v2", PollIntervalSeconds: 300, ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	profile, poll, active := "statusio", 120, 30
	if err := st.UpdateService(ctx, slug, ServicePatch{
		Profile:               &profile,
		PollIntervalSeconds:   &poll,
		ActiveIntervalSeconds: &active,
	}); err != nil {
		t.Fatalf("UpdateService: %v", err)
	}

	row, err := st.AdminGetService(ctx, slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if row.Profile != "statusio" || row.PollIntervalSeconds != 120 || row.ActiveIntervalSeconds != 30 {
		t.Errorf("profile/intervals = %q/%d/%d, want statusio/120/30",
			row.Profile, row.PollIntervalSeconds, row.ActiveIntervalSeconds)
	}
}
