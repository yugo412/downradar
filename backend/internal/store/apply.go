package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"downradar.info/backend/internal/adapters"
)

func (s *Store) ApplyReport(ctx context.Context, serviceID int64, rep adapters.Report) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("apply: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	changedAt := rep.FetchedAt
	if changedAt.IsZero() {
		changedAt = time.Now().UTC()
	}

	if err := s.guardPageIdentity(ctx, tx, serviceID, rep.PageID); err != nil {
		return false, err
	}
	if err := s.storeRawPayload(ctx, tx, serviceID, changedAt, rep); err != nil {
		return false, err
	}

	changed := false
	seen := make([]string, 0, len(rep.Components))
	unknownStatuses := make([]string, 0)

	for _, c := range rep.Components {
		if c.SourceID == "" {
			continue
		}
		seen = append(seen, c.SourceID)

		var compID int64
		err := tx.QueryRow(ctx, `
			insert into components (service_id, source_id, name, group_name, position, active, updated_at)
			values ($1, $2, $3, $4, $5, true, now())
			on conflict (service_id, source_id) do update set
				name       = excluded.name,
				group_name = excluded.group_name,
				position   = excluded.position,
				active     = true,
				updated_at = now()
			returning id`,
			serviceID, c.SourceID, c.Name, nullify(c.Group), c.Position,
		).Scan(&compID)
		if err != nil {
			return false, fmt.Errorf("apply: upsert component %s: %w", c.SourceID, err)
		}

		if c.Status == adapters.HealthUnknown {
			unknownStatuses = append(unknownStatuses, c.Name)
		}

		didChange, err := s.applyComponentState(ctx, tx, serviceID, compID, c.Status, changedAt, rep.SourceUpdatedAt)
		if err != nil {
			return false, err
		}
		changed = changed || didChange
	}

	if len(seen) > 0 {
		if _, err := tx.Exec(ctx, `
			update components set active = false, updated_at = now()
			where service_id = $1 and active and source_id <> all($2)`, serviceID, seen,
		); err != nil {
			return false, fmt.Errorf("apply: deactivate removed components: %w", err)
		}
	}

	didChange, err := s.applyServiceState(ctx, tx, serviceID, rep.Global, changedAt, rep.SourceUpdatedAt)
	if err != nil {
		return false, err
	}
	changed = changed || didChange

	for _, inc := range rep.Incidents {
		if inc.SourceID == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			insert into incidents (service_id, source_id, name, status, impact,
			                       started_at, resolved_at, url, updated_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, now())
			on conflict (service_id, source_id) do update set
				name        = excluded.name,
				status      = excluded.status,
				impact      = excluded.impact,
				resolved_at = excluded.resolved_at,
				url         = excluded.url,
				updated_at  = now()`,
			serviceID, inc.SourceID, inc.Name, inc.Status, nullify(inc.Impact),
			nullTime(inc.StartedAt), inc.ResolvedAt, nullify(inc.URL),
		); err != nil {
			return false, fmt.Errorf("apply: upsert incident %s: %w", inc.SourceID, err)
		}
	}

	if _, err := tx.Exec(ctx,
		`update services set last_polled_at = now() where id = $1`, serviceID,
	); err != nil {
		return false, fmt.Errorf("apply: update last_polled_at: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		insert into poll_runs (service_id, ok, http_status, components_found, unknown_statuses)
		values ($1, true, $2, $3, $4)`,
		serviceID, rep.HTTPStatus, len(rep.Components), unknownStatuses,
	); err != nil {
		return false, fmt.Errorf("apply: insert poll_run: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("apply: commit: %w", err)
	}
	return changed, nil
}

func (s *Store) applyComponentState(
	ctx context.Context, tx pgx.Tx, serviceID, compID int64,
	status adapters.Health, changedAt, sourceChangedAt time.Time,
) (bool, error) {
	var prev string
	err := tx.QueryRow(ctx,
		`select status from component_state where component_id = $1`, compID,
	).Scan(&prev)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `
			insert into component_state (component_id, status, changed_at)
			values ($1, $2, $3)`, compID, string(status), changedAt); err != nil {
			return false, fmt.Errorf("apply: insert component_state: %w", err)
		}
		return true, s.insertEvent(ctx, tx, serviceID, &compID, status, changedAt, sourceChangedAt)

	case err != nil:
		return false, fmt.Errorf("apply: read component_state: %w", err)

	case prev == string(status):
		return false, nil

	default:
		if _, err := tx.Exec(ctx, `
			update component_state set status = $2, changed_at = $3, updated_at = now()
			where component_id = $1`, compID, string(status), changedAt); err != nil {
			return false, fmt.Errorf("apply: update component_state: %w", err)
		}
		return true, s.insertEvent(ctx, tx, serviceID, &compID, status, changedAt, sourceChangedAt)
	}
}

func (s *Store) applyServiceState(
	ctx context.Context, tx pgx.Tx, serviceID int64,
	status adapters.Health, changedAt, sourceChangedAt time.Time,
) (bool, error) {
	var prev string
	err := tx.QueryRow(ctx,
		`select status from service_state where service_id = $1`, serviceID,
	).Scan(&prev)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `
			insert into service_state (service_id, status, changed_at)
			values ($1, $2, $3)`, serviceID, string(status), changedAt); err != nil {
			return false, fmt.Errorf("apply: insert service_state: %w", err)
		}
		return true, s.insertEvent(ctx, tx, serviceID, nil, status, changedAt, sourceChangedAt)

	case err != nil:
		return false, fmt.Errorf("apply: read service_state: %w", err)

	case prev == string(status):
		return false, nil

	default:
		if _, err := tx.Exec(ctx, `
			update service_state set status = $2, changed_at = $3, updated_at = now()
			where service_id = $1`, serviceID, string(status), changedAt); err != nil {
			return false, fmt.Errorf("apply: update service_state: %w", err)
		}
		return true, s.insertEvent(ctx, tx, serviceID, nil, status, changedAt, sourceChangedAt)
	}
}

func (s *Store) insertEvent(
	ctx context.Context, tx pgx.Tx, serviceID int64, compID *int64,
	status adapters.Health, changedAt, sourceChangedAt time.Time,
) error {
	_, err := tx.Exec(ctx, `
		insert into status_events (service_id, component_id, status, changed_at, source_changed_at)
		values ($1, $2, $3, $4, $5)`,
		serviceID, compID, string(status), changedAt, nullTime(sourceChangedAt))
	if err != nil {
		return fmt.Errorf("apply: insert status_event: %w", err)
	}
	return nil
}

func (s *Store) RecordFailure(ctx context.Context, serviceID int64, httpStatus int, msg string, durationMS int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		insert into poll_runs (service_id, ok, http_status, error, duration_ms)
		values ($1, false, $2, $3, $4)`,
		serviceID, nullInt(httpStatus), msg, durationMS,
	); err != nil {
		return fmt.Errorf("apply: record failure: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`update services set last_polled_at = now() where id = $1`, serviceID,
	); err != nil {
		return fmt.Errorf("apply: update last_polled_at: %w", err)
	}
	return tx.Commit(ctx)
}
