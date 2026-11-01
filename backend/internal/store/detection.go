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

// DetectionAttemptLimit is how many failures in a row the queue allows before
// it pauses a row. A page nothing reads is usually a wrong address or a
// platform without an adapter, and both are for a person to fix.
const DetectionAttemptLimit = 20

// PendingDetection returns the vendors that have no profile and therefore
// cannot be polled. The caller decides when to try again from LastPolledAt.
func (s *Store) PendingDetection(ctx context.Context) ([]DetectRow, error) {
	rows, err := s.pool.Query(ctx, `
		select s.id, s.slug, s.name, coalesce(s.url, ''), s.last_polled_at
		from services s
		where s.active
		  and not s.paused
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
// come from the profile, which is where they are declared. A match is also a
// manual start, so the pause and the failure count are cleared with it.
func (s *Store) SetDetectedProfile(ctx context.Context, id int64, profile string, pollSeconds, activeSeconds int) error {
	_, err := s.pool.Exec(ctx, `
		update services set
			profile                 = $2,
			poll_interval_seconds   = $3,
			active_interval_seconds = $4,
			status                  = $5,
			paused                  = false,
			detection_attempts      = 0,
			updated_at              = now()
		where id = $1`,
		id, profile, pollSeconds, activeSeconds, string(StatusPublished))
	if err != nil {
		return fmt.Errorf("services: set detected profile %d: %w", id, err)
	}
	return nil
}

// ResetDetectionAttempts starts the failure count over after a person asks for
// another try. Retry keeps an existing pause, so only a successful match or a
// resume lifts it.
func (s *Store) ResetDetectionAttempts(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		update services set detection_attempts = 0, updated_at = now() where id = $1`, id)
	if err != nil {
		return fmt.Errorf("services: reset detection attempts %d: %w", id, err)
	}
	return nil
}

// ParkDetection records that the queue ran out of profiles: the row becomes
// no_match so polling leaves it alone, and the attempt goes to poll_runs. The
// failures are counted in a row, and the row is paused once the count reaches
// DetectionAttemptLimit. Both halves share a transaction, so a read that found
// nothing is still recorded.
func (s *Store) ParkDetection(ctx context.Context, id int64, reason string) (attempts int, paused bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("services: park detection %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		insert into poll_runs (service_id, ok, error)
		values ($1, false, $2)`, id, reason); err != nil {
		return 0, false, fmt.Errorf("services: park detection %d: %w", id, err)
	}
	err = tx.QueryRow(ctx, `
		update services set
			status             = $2,
			last_polled_at     = now(),
			detection_attempts = detection_attempts + 1,
			paused             = paused or detection_attempts + 1 >= $3,
			updated_at         = now()
		where id = $1
		returning detection_attempts, paused`,
		id, string(StatusNoMatch), DetectionAttemptLimit).Scan(&attempts, &paused)
	if err != nil {
		return 0, false, fmt.Errorf("services: park detection %d: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("services: park detection %d: %w", id, err)
	}
	return attempts, paused, nil
}
