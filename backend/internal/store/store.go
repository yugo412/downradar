package store

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"downradar.info/backend/db"
)

type Store struct {
	pool                  *pgxpool.Pool
	rawPayloadMinInterval time.Duration
	defaultLimits         map[string]int
}

type Options struct {
	RawPayloadMinInterval time.Duration
	// DefaultLimits is the starting value a new account is given for each kind
	// of limit, keyed by the Limit* constants. The values come from the
	// environment; what is read afterwards is the row.
	DefaultLimits map[string]int
}

func New(ctx context.Context, dsn string, opts Options) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	interval := opts.RawPayloadMinInterval
	if interval <= 0 {
		interval = time.Hour
	}
	return &Store{
		pool:                  pool,
		rawPayloadMinInterval: interval,
		defaultLimits:         opts.DefaultLimits,
	}, nil
}

func (s *Store) Close() { s.pool.Close() }

// migrationLockID is arbitrary but fixed. Two migrators agree on it so that only
// one of them works at a time.
const migrationLockID int64 = 85720260116

func (s *Store) Migrate(ctx context.Context) error {
	// One migrator at a time. Two processes starting together, or the test
	// packages of one `go test ./...` sharing a database, would otherwise both
	// decide that a migration is missing and race to apply and record it.
	//
	// The lock is taken and released on one connection, because a session level
	// advisory lock belongs to the session that took it.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire a connection for the migration lock: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `select pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("store: take the migration lock: %w", err)
	}
	defer func() {
		// Released on the way out whatever happened, so the next migrator is not
		// left waiting for this one.
		_, _ = conn.Exec(context.Background(), `select pg_advisory_unlock($1)`, migrationLockID)
	}()

	_, err = s.pool.Exec(ctx, `
		create table if not exists schema_migrations (
			version    text primary key,
			applied_at timestamptz not null default now()
		)`)
	if err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(db.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var applied bool
		if err := s.pool.QueryRow(ctx,
			`select exists (select 1 from schema_migrations where version = $1)`, name,
		).Scan(&applied); err != nil {
			return fmt.Errorf("store: check migration %s: %w", name, err)
		}
		if applied {
			continue
		}

		sqlBytes, err := db.Migrations.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: read migration %s: %w", name, err)
		}

		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("store: begin tx for %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("store: apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`insert into schema_migrations (version) values ($1)`, name,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("store: record migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("store: commit migration %s: %w", name, err)
		}
	}
	return nil
}
