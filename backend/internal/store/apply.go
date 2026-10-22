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
		`update services set last_polled_at = now(), next_poll_at = null, poll_refusals = 0
		 where id = $1`, serviceID,
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
		return true, s.insertEvent(ctx, tx, serviceID, &compID, "", status, changedAt, sourceChangedAt)

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
		if err := s.enqueueComponentAlerts(ctx, tx, serviceID, compID, prev, string(status), changedAt); err != nil {
			return false, err
		}
		return true, s.insertEvent(ctx, tx, serviceID, &compID, prev, status, changedAt, sourceChangedAt)
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
		return true, s.insertEvent(ctx, tx, serviceID, nil, "", status, changedAt, sourceChangedAt)

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
		if err := s.enqueueAlerts(ctx, tx, serviceID, prev, string(status), changedAt); err != nil {
			return false, err
		}
		return true, s.insertEvent(ctx, tx, serviceID, nil, prev, status, changedAt, sourceChangedAt)
	}
}

// enqueueAlerts writes one delivery per live channel for a vendor whose own
// status has changed. An email, a chat or a Slack channel is written one only if
// the subscriber is watching the vendor as a whole, because those ways report on
// what was picked and that is their set of targets. A webhook is written one
// whenever the vendor changes at all, since its body says what the vendor looks
// like now rather than only what moved. It runs in the same transaction as the
// change itself, so an alert cannot be lost with a crash and cannot be written
// twice by a retry.
//
// A subscription that watches one component of the vendor is left out of the
// messages about the vendor: what it asked about is that component, so the change
// it waits for is the component's own, which enqueueComponentAlerts writes.
//
// The first state a service is ever seen in is not a change: nobody is told that
// a vendor they subscribed to was, as far as we knew, already up. Only a move
// from one status to another is worth a message.
func (s *Store) enqueueAlerts(ctx context.Context, tx pgx.Tx, serviceID int64, from, to string, changedAt time.Time) error {
	if _, err := tx.Exec(ctx, `
		insert into alert_deliveries (channel_id, service_id, component_id, from_status, to_status, changed_at)
		select c.id, a.service_id, null, $2, $3, $4
		from alert_subscriptions a
		join alert_channels c on c.subscription_id = a.id
		where a.service_id = $1
		  and c.unsubscribed_at is null and c.paused_at is null
		  and (
			c.kind = 'webhook'
			or exists (
				select 1 from alert_targets t
				where t.subscription_id = a.id and t.service_id = $1 and t.component_id is null)
		  )`,
		serviceID, from, to, changedAt); err != nil {
		return fmt.Errorf("apply: enqueue alerts: %w", err)
	}
	return nil
}

// enqueueComponentAlerts is the same bargain for one component: an email, a chat
// or a Slack channel is written a delivery if that component is one of the things
// its subscriber picked, and a webhook is written one for any component of the
// vendor, because its body carries them all. A component's first state is the baseline the rest
// are read against, not a change, and that is why the caller only reaches here
// when it was already known in another state.
func (s *Store) enqueueComponentAlerts(ctx context.Context, tx pgx.Tx, serviceID, componentID int64, from, to string, changedAt time.Time) error {
	if _, err := tx.Exec(ctx, `
		insert into alert_deliveries (channel_id, service_id, component_id, from_status, to_status, changed_at)
		select c.id, a.service_id, $2, $3, $4, $5
		from alert_subscriptions a
		join alert_channels c on c.subscription_id = a.id
		where a.service_id = $1
		  and c.unsubscribed_at is null and c.paused_at is null
		  and (
			c.kind = 'webhook'
			or exists (
				select 1 from alert_targets t
				where t.subscription_id = a.id and t.component_id = $2)
		  )`,
		serviceID, componentID, from, to, changedAt); err != nil {
		return fmt.Errorf("apply: enqueue component alerts: %w", err)
	}
	return nil
}

func (s *Store) insertEvent(
	ctx context.Context, tx pgx.Tx, serviceID int64, compID *int64,
	prev string, status adapters.Health, changedAt, sourceChangedAt time.Time,
) error {
	// The first state a target is ever seen in has nothing before it, and that is
	// written as a null rather than as an empty status.
	var previous any
	if prev != "" {
		previous = prev
	}

	_, err := tx.Exec(ctx, `
		insert into status_events (service_id, component_id, previous_status, status, changed_at, source_changed_at)
		values ($1, $2, $3, $4, $5, $6)`,
		serviceID, compID, previous, string(status), changedAt, nullTime(sourceChangedAt))
	if err != nil {
		return fmt.Errorf("apply: insert status_event: %w", err)
	}
	return nil
}

// PollFailure is one failed fetch as the vendor's schedule sees it. NextPollAt
// and Refusals are only set when the page refused to answer, so a plain failure
// clears any wait that an earlier refusal left behind: the page did answer.
type PollFailure struct {
	HTTPStatus int
	Error      string
	DurationMS int
	NextPollAt *time.Time
	Refusals   int
}

func (s *Store) RecordFailure(ctx context.Context, serviceID int64, f PollFailure) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		insert into poll_runs (service_id, ok, http_status, error, duration_ms)
		values ($1, false, $2, $3, $4)`,
		serviceID, nullInt(f.HTTPStatus), f.Error, f.DurationMS,
	); err != nil {
		return fmt.Errorf("apply: record failure: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update services set last_polled_at = now(), next_poll_at = $2, poll_refusals = $3
		where id = $1`, serviceID, f.NextPollAt, f.Refusals,
	); err != nil {
		return fmt.Errorf("apply: update last_polled_at: %w", err)
	}
	return tx.Commit(ctx)
}
