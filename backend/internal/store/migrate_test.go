package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"downradar.info/backend/db"
)

// TestMigrateIsSafeToRunAtTheSameTime reproduces what the pipeline does: a
// database with nothing applied yet, and several migrators starting at once
// because `go test ./...` runs its packages in parallel. Without the lock they
// all decide a migration is missing and race to record it.
//
// The race needs an empty database, so this works in a schema of its own rather
// than the one the rest of the tests share.
func TestMigrateIsSafeToRunAtTheSameTime(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set, skipping the integration test")
	}

	ctx := context.Background()
	admin, err := New(ctx, dsn, Options{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(admin.Close)

	schema := fmt.Sprintf("zz_migrate_%d", time.Now().UnixNano())
	if _, err := admin.pool.Exec(ctx, `create schema `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.pool.Exec(context.Background(), `drop schema `+schema+` cascade`)
	})

	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	fresh := dsn + separator + "search_path=" + schema

	const migrators = 3
	stores := make([]*Store, migrators)
	errs := make([]error, migrators)

	for i := range stores {
		st, err := New(ctx, fresh, Options{})
		if err != nil {
			t.Fatalf("connect %d: %v", i, err)
		}
		t.Cleanup(st.Close)
		stores[i] = st
	}

	var wg sync.WaitGroup
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = stores[i].Migrate(ctx)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("migrator %d: %v", i, err)
		}
	}

	// Every migration is recorded once, and the tables they create are there.
	entries, err := fs.ReadDir(db.Migrations, "migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	want := 0
	for _, e := range entries {
		if !e.IsDir() {
			want++
		}
	}

	var applied int
	if err := admin.pool.QueryRow(ctx,
		`select count(*) from `+schema+`.schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count applied: %v", err)
	}
	if applied != want {
		t.Errorf("applied = %d, want %d", applied, want)
	}
}
