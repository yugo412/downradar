package poller

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/store"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testProfiles() catalog.Profiles {
	return catalog.Profiles{
		"statuspage-v2": {
			Kind:                  "statuspage",
			Endpoint:              "/api/v2/summary.json",
			PollIntervalSeconds:   300,
			ActiveIntervalSeconds: 60,
		},
	}
}

func row(slug, profile string) store.ServiceRow {
	return store.ServiceRow{
		ID:                    1,
		Slug:                  slug,
		Name:                  slug,
		URL:                   "https://" + slug + ".example.com",
		Profile:               profile,
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
		Status:                adapters.HealthOK,
	}
}

func TestResolveBuildsTargetsFromRows(t *testing.T) {
	p := &Poller{profiles: testProfiles(), log: quietLogger()}

	jobs := p.resolve([]store.ServiceRow{row("a", "statuspage-v2")})
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	target := jobs[0].target
	if target.Kind != "statuspage" {
		t.Errorf("kind = %q, want it resolved from the profile", target.Kind)
	}
	if target.Endpoint != "/api/v2/summary.json" {
		t.Errorf("endpoint = %q, want it inherited from the profile", target.Endpoint)
	}
	if jobs[0].row.ID != 1 {
		t.Errorf("the row id must be carried through, got %d", jobs[0].row.ID)
	}
}

func TestResolvePrefersTheRowEndpoint(t *testing.T) {
	p := &Poller{profiles: testProfiles(), log: quietLogger()}

	r := row("a", "statuspage-v2")
	r.Endpoint = "/custom/summary.json"
	jobs := p.resolve([]store.ServiceRow{r})

	if jobs[0].target.Endpoint != "/custom/summary.json" {
		t.Errorf("endpoint = %q, want the row to win", jobs[0].target.Endpoint)
	}
}

func TestResolveSkipsUnknownProfiles(t *testing.T) {
	p := &Poller{profiles: testProfiles(), log: quietLogger()}

	jobs := p.resolve([]store.ServiceRow{
		row("known", "statuspage-v2"),
		row("orphan", "deleted-profile"),
		row("no-profile", ""),
	})

	if len(jobs) != 1 || jobs[0].row.Slug != "known" {
		t.Fatalf("only the resolvable vendor should be polled, got %+v", jobs)
	}
}

func TestDueJobsUsesTheActiveIntervalWhenUnhealthy(t *testing.T) {
	now := time.Now().UTC()
	recent := now.Add(-90 * time.Second)

	healthy := row("healthy", "statuspage-v2")
	healthy.LastPolledAt = &recent

	unhealthy := row("unhealthy", "statuspage-v2")
	unhealthy.Status = adapters.HealthMajor
	unhealthy.LastPolledAt = &recent

	p := &Poller{profiles: testProfiles(), log: quietLogger()}
	due := dueJobs(p.resolve([]store.ServiceRow{healthy, unhealthy}), now)

	if len(due) != 1 || due[0].row.Slug != "unhealthy" {
		t.Fatalf("the unhealthy vendor should be due after 90s and the healthy one not, got %+v", due)
	}
}

func TestDueJobsPollsVendorsThatNeverRan(t *testing.T) {
	p := &Poller{profiles: testProfiles(), log: quietLogger()}

	due := dueJobs(p.resolve([]store.ServiceRow{row("new", "statuspage-v2")}), time.Now().UTC())
	if len(due) != 1 {
		t.Fatalf("a vendor with no last poll must be due, got %d", len(due))
	}
}
