package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

func integrationStoreWith(t *testing.T, opts Options) *Store {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set, skipping the integration test")
	}

	ctx := context.Background()
	st, err := New(ctx, dsn, opts)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// integrationStore connects to the database named by DATABASE_URL and applies
// the migrations. CI gives it an empty database, so a test must create every row
// it needs and never assume the development seed is loaded.
func integrationStore(t *testing.T) *Store {
	t.Helper()
	return integrationStoreWith(t, Options{})
}

func testService(t *testing.T, st *Store, slug string) int64 {
	t.Helper()

	ctx := context.Background()
	var id int64
	err := st.pool.QueryRow(ctx, `
		insert into services (slug, name, url, active, page_id, allow_shared_page)
		values ($1, $1, 'https://status.example.com', true, null, false)
		on conflict (slug) do update set
			active            = true,
			page_id           = null,
			allow_shared_page = false
		returning id`, slug).Scan(&id)
	if err != nil {
		t.Fatalf("create test service %s: %v", slug, err)
	}

	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(), `delete from services where slug = $1`, slug)
	})
	return id
}

func setShared(t *testing.T, st *Store, slug string, shared bool) {
	t.Helper()
	if _, err := st.pool.Exec(context.Background(),
		`update services set allow_shared_page = $2 where slug = $1`, slug, shared); err != nil {
		t.Fatalf("set shared_page on %s: %v", slug, err)
	}
}

func pageID(t *testing.T, st *Store, slug string) string {
	t.Helper()
	var value string
	if err := st.pool.QueryRow(context.Background(),
		`select coalesce(page_id, '') from services where slug = $1`, slug).Scan(&value); err != nil {
		t.Fatalf("read page_id of %s: %v", slug, err)
	}
	return value
}

func testReport(pageID string) adapters.Report {
	return adapters.Report{
		Global:    adapters.HealthOK,
		PageID:    pageID,
		FetchedAt: time.Now().UTC(),
		Components: []adapters.ComponentStatus{
			{SourceID: "c1", Name: "Web", Position: 1, Status: adapters.HealthOK},
		},
	}
}

func TestPageIdentityIsPinnedOnFirstPoll(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	id := testService(t, st, "zz-test-guard-pin")

	if _, err := st.ApplyReport(ctx, id, testReport("page-alpha")); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if got := pageID(t, st, "zz-test-guard-pin"); got != "page-alpha" {
		t.Errorf("page_id = %q, want page-alpha", got)
	}
}

func TestPageIdentityRefusesAChangedPage(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	id := testService(t, st, "zz-test-guard-change")

	if _, err := st.ApplyReport(ctx, id, testReport("page-alpha")); err != nil {
		t.Fatalf("first poll: %v", err)
	}

	_, err := st.ApplyReport(ctx, id, testReport("page-beta"))
	if err == nil {
		t.Fatal("expected a refusal when the status page changes")
	}
	if !strings.Contains(err.Error(), "pinned") {
		t.Errorf("error = %q, want it to mention the pin", err)
	}
	if got := pageID(t, st, "zz-test-guard-change"); got != "page-alpha" {
		t.Errorf("page_id = %q, want it to stay page-alpha", got)
	}
}

func TestPageIdentityRefusesAnUndeclaredCollision(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	owner := testService(t, st, "zz-test-guard-owner")
	intruder := testService(t, st, "zz-test-guard-intruder")

	if _, err := st.ApplyReport(ctx, owner, testReport("page-shared")); err != nil {
		t.Fatalf("owner poll: %v", err)
	}

	_, err := st.ApplyReport(ctx, intruder, testReport("page-shared"))
	if err == nil {
		t.Fatal("expected a refusal when two vendors report the same page")
	}
	if !strings.Contains(err.Error(), "same status page") {
		t.Errorf("error = %q, want it to mention the shared page", err)
	}
	if got := pageID(t, st, "zz-test-guard-intruder"); got != "" {
		t.Errorf("the intruder must not be pinned, got %q", got)
	}
}

func TestPageIdentityAllowsADeclaredCollision(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	owner := testService(t, st, "zz-test-guard-twin-a")
	twin := testService(t, st, "zz-test-guard-twin-b")

	if _, err := st.ApplyReport(ctx, owner, testReport("page-twin")); err != nil {
		t.Fatalf("first twin: %v", err)
	}

	if _, err := st.ApplyReport(ctx, twin, testReport("page-twin")); err == nil {
		t.Fatal("expected a refusal before the sharing is declared")
	}

	setShared(t, st, "zz-test-guard-twin-a", true)
	setShared(t, st, "zz-test-guard-twin-b", true)

	if _, err := st.ApplyReport(ctx, twin, testReport("page-twin")); err != nil {
		t.Fatalf("declared sharing should be allowed: %v", err)
	}
	if got := pageID(t, st, "zz-test-guard-twin-b"); got != "page-twin" {
		t.Errorf("page_id = %q, want page-twin", got)
	}
}

func TestPageIdentityIgnoresAdaptersWithoutAnIdentity(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	first := testService(t, st, "zz-test-guard-anon-a")
	second := testService(t, st, "zz-test-guard-anon-b")

	for _, id := range []int64{first, second} {
		if _, err := st.ApplyReport(ctx, id, testReport("")); err != nil {
			t.Fatalf("a report without a page id must be accepted: %v", err)
		}
	}

	if got := pageID(t, st, "zz-test-guard-anon-a"); got != "" {
		t.Errorf("page_id = %q, want it to stay empty", got)
	}
}

func TestRawPayloadIsStoredGzipped(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	id := testService(t, st, "zz-test-guard-raw")

	rep := testReport("")
	rep.Raw = []byte(strings.Repeat("status page payload ", 100))

	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("ApplyReport: %v", err)
	}

	var stored []byte
	if err := st.pool.QueryRow(ctx,
		`select body from raw_payloads where service_id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read raw payload: %v", err)
	}
	if len(stored) == 0 {
		t.Fatal("the raw payload was not stored")
	}
	if len(stored) >= len(rep.Raw) {
		t.Errorf("stored size %d should be smaller than the original %d", len(stored), len(rep.Raw))
	}

	reader, err := gzip.NewReader(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("stored payload is not gzip: %v", err)
	}
	defer reader.Close()

	restored, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(restored, rep.Raw) {
		t.Errorf("round trip changed the payload: got %d bytes, want %d", len(restored), len(rep.Raw))
	}
}

func TestRawPayloadSkipsIdenticalContent(t *testing.T) {
	st := integrationStoreWith(t, Options{RawPayloadMinInterval: time.Nanosecond})
	ctx := context.Background()
	id := testService(t, st, "zz-test-guard-dedupe")

	rep := testReport("")
	rep.Raw = []byte("identical payload")

	for i := 0; i < 3; i++ {
		if _, err := st.ApplyReport(ctx, id, rep); err != nil {
			t.Fatalf("ApplyReport %d: %v", i, err)
		}
	}

	var count int
	if err := st.pool.QueryRow(ctx,
		`select count(*) from raw_payloads where service_id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("three identical polls stored %d rows, want 1", count)
	}

	rep.Raw = []byte("a payload that changed")
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("ApplyReport after change: %v", err)
	}

	if err := st.pool.QueryRow(ctx,
		`select count(*) from raw_payloads where service_id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("after a change there should be 2 rows, got %d", count)
	}
}

func TestRawPayloadIsThrottledByInterval(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	id := testService(t, st, "zz-test-guard-throttle")

	rep := testReport("")
	rep.Raw = []byte("first payload")
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("first ApplyReport: %v", err)
	}

	rep.Raw = []byte("a completely different payload")
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("second ApplyReport: %v", err)
	}

	var count int
	if err := st.pool.QueryRow(ctx,
		`select count(*) from raw_payloads where service_id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("a second poll inside the interval stored %d rows, want 1", count)
	}
}

func TestDeleteRawPayloadsOlderThan(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	id := testService(t, st, "zz-test-guard-retention")

	rep := testReport("")
	rep.Raw = []byte("payload")
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("ApplyReport: %v", err)
	}

	if _, err := st.pool.Exec(ctx,
		`update raw_payloads set fetched_at = now() - interval '30 days' where service_id = $1`, id); err != nil {
		t.Fatalf("age the payload: %v", err)
	}

	if _, err := st.DeleteRawPayloadsOlderThan(ctx, 7*24*time.Hour); err != nil {
		t.Fatalf("delete old payloads: %v", err)
	}

	var remaining int
	if err := st.pool.QueryRow(ctx,
		`select count(*) from raw_payloads where service_id = $1`, id).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}
}
