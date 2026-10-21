package store

import (
	"context"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
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

// scheduleOf reads what the poller will look at on its next cycle.
func scheduleOf(t *testing.T, st *Store, id int64) (next *time.Time, refusals int, last *time.Time) {
	t.Helper()

	if err := st.pool.QueryRow(context.Background(),
		`select next_poll_at, poll_refusals, last_polled_at from services where id = $1`, id,
	).Scan(&next, &refusals, &last); err != nil {
		t.Fatalf("read schedule: %v", err)
	}
	return next, refusals, last
}

func TestRecordFailureRemembersTheWait(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := testService(t, st, uniqueName("poll-backoff"))
	until := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Millisecond)

	if err := st.RecordFailure(ctx, id, PollFailure{
		HTTPStatus: 429,
		Error:      "the page asked to be left alone",
		DurationMS: 42,
		NextPollAt: &until,
		Refusals:   2,
	}); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	next, refusals, last := scheduleOf(t, st, id)
	if next == nil || !next.UTC().Equal(until) {
		t.Errorf("next poll at %v, want %v", next, until)
	}
	if refusals != 2 {
		t.Errorf("refusals = %d, want 2", refusals)
	}
	if last == nil {
		t.Error("a failed poll should still move last_polled_at")
	}

	// A page that answers, even badly, is not refusing: the wait is dropped and
	// the next refusal starts the curve again.
	if err := st.RecordFailure(ctx, id, PollFailure{HTTPStatus: 500, Error: "boom"}); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if next, refusals, _ := scheduleOf(t, st, id); next != nil || refusals != 0 {
		t.Errorf("next poll = %v, refusals = %d, want no wait", next, refusals)
	}
}

func TestApplyReportClearsTheWait(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := testService(t, st, uniqueName("poll-recovered"))
	until := time.Now().UTC().Add(time.Hour)
	if err := st.RecordFailure(ctx, id, PollFailure{
		HTTPStatus: 403,
		Error:      "the page turned the poller away",
		NextPollAt: &until,
		Refusals:   4,
	}); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	if _, err := st.ApplyReport(ctx, id, adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: 200,
		Global:     adapters.HealthOK,
	}); err != nil {
		t.Fatalf("ApplyReport: %v", err)
	}

	if next, refusals, _ := scheduleOf(t, st, id); next != nil || refusals != 0 {
		t.Errorf("next poll = %v, refusals = %d, want a clean schedule after a good poll", next, refusals)
	}
}
