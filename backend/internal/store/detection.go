package store

import (
	"context"
	"fmt"
	"time"
)

// DetectRow is a vendor waiting for a profile: it is live, but no adapter reads
// its page yet.
type DetectRow struct {
	ID           int64
	Slug         string
	Name         string
	URL          string
	LastPolledAt *time.Time
}

// PendingDetection returns the vendors that have no profile and therefore
// cannot be polled. The caller decides when to try again from LastPolledAt.
func (s *Store) PendingDetection(ctx context.Context) ([]DetectRow, error) {
	rows, err := s.pool.Query(ctx, `
		select s.id, s.slug, s.name, coalesce(s.url, ''), s.last_polled_at
		from services s
		where s.active
		  and s.status in ($1, $2)
		  and coalesce(s.profile, '') = ''
		  and coalesce(s.url, '') <> ''
		order by s.slug`, string(StatusPublished), string(StatusNoMatch))
	if err != nil {
		return nil, fmt.Errorf("services: pending detection: %w", err)
	}
	defer rows.Close()

	var out []DetectRow
	for rows.Next() {
		var r DetectRow
		if err := rows.Scan(&r.ID, &r.Slug, &r.Name, &r.URL, &r.LastPolledAt); err != nil {
			return nil, fmt.Errorf("services: pending detection scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetDetectedProfile records the profile detection settled on and puts the
// vendor live, so the next poll pass treats it like any other row. The intervals
// come from the profile, which is where they are declared.
func (s *Store) SetDetectedProfile(ctx context.Context, id int64, profile string, pollSeconds, activeSeconds int) error {
	_, err := s.pool.Exec(ctx, `
		update services set
			profile                 = $2,
			poll_interval_seconds   = $3,
			active_interval_seconds = $4,
			status                  = $5,
			updated_at              = now()
		where id = $1`,
		id, profile, pollSeconds, activeSeconds, string(StatusPublished))
	if err != nil {
		return fmt.Errorf("services: set detected profile %d: %w", id, err)
	}
	return nil
}

// ParkDetection records that the queue ran out of profiles on this vendor: the
// row is marked no_match so the poll pass leaves it alone, the retry clock
// moves, and the attempt is written to poll_runs so the fetch log shows the page
// was read and came back with nothing. Both halves are one transaction: a vendor
// that was read for nothing must not look like one that was never read, nor the
// other way round.
func (s *Store) ParkDetection(ctx context.Context, id int64, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("services: park detection %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		insert into poll_runs (service_id, ok, error)
		values ($1, false, $2)`, id, reason); err != nil {
		return fmt.Errorf("services: park detection %d: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `
		update services set
			status         = $2,
			last_polled_at = now(),
			updated_at     = now()
		where id = $1`, id, string(StatusNoMatch)); err != nil {
		return fmt.Errorf("services: park detection %d: %w", id, err)
	}
	return tx.Commit(ctx)
}
