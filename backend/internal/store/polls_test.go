package store

import (
	"context"
	"testing"
	"time"
)

func TestRecentPollRunsNewestFirst(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug := uniqueName("poll-log")
	id := testService(t, st, slug)

	insert := func(ok bool, errText string, duration *int, at time.Time) {
		t.Helper()

		var status *int
		if !ok {
			httpStatus := 502
			status = &httpStatus
		}
		if _, err := st.pool.Exec(ctx, `
			insert into poll_runs (service_id, ok, http_status, components_found, error, duration_ms, created_at)
			values ($1, $2, $3, 2, nullif($4, ''), $5, $6)`,
			id, ok, status, errText, duration, at); err != nil {
			t.Fatalf("insert poll run: %v", err)
		}
	}

	now := time.Now().Truncate(time.Millisecond)
	slow := 1500
	insert(true, "", nil, now.Add(-3*time.Minute))
	insert(false, "unexpected status 503", &slow, now.Add(-time.Minute))
	insert(true, "", nil, now)

	runs, err := st.RecentPollRuns(ctx, slug, 10)
	if err != nil {
		t.Fatalf("RecentPollRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d poll runs, want 3", len(runs))
	}
	if !runs[0].OK || runs[1].OK || !runs[2].OK {
		t.Errorf("result flags = %v, %v, %v, want the middle run to be the failure",
			runs[0].OK, runs[1].OK, runs[2].OK)
	}
	if runs[1].HTTPStatus == nil || *runs[1].HTTPStatus != 502 {
		t.Errorf("http status = %v, want 502", runs[1].HTTPStatus)
	}
	if runs[1].Error == "" {
		t.Error("expected the failed run to carry its error")
	}
	if runs[1].DurationMS == nil || *runs[1].DurationMS != slow {
		t.Errorf("duration = %v, want %d", runs[1].DurationMS, slow)
	}
	if runs[0].DurationMS != nil {
		t.Errorf("duration on a successful run = %d, want none", *runs[0].DurationMS)
	}
	if got := runs[0].FetchedAt.UTC().Truncate(time.Millisecond); !got.Equal(now.UTC()) {
		t.Errorf("newest run fetched at %v, want %v", got, now)
	}

	limited, err := st.RecentPollRuns(ctx, slug, 1)
	if err != nil {
		t.Fatalf("RecentPollRuns with a limit: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("got %d poll runs with limit 1, want 1", len(limited))
	}
	if !limited[0].FetchedAt.Equal(runs[0].FetchedAt) {
		t.Errorf("limit kept the wrong end: %v, want %v", limited[0].FetchedAt, runs[0].FetchedAt)
	}

	missing, err := st.RecentPollRuns(ctx, uniqueName("no-such"), 10)
	if err != nil {
		t.Fatalf("RecentPollRuns for an unknown service: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("got %d poll runs for an unknown service, want 0", len(missing))
	}
}
