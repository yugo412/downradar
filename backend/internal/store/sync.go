package store

import (
	"context"
	"fmt"
	"time"

	"downradar.info/backend/internal/adapters"
)

func (s *Store) SyncServices(ctx context.Context, targets []adapters.Target) (map[string]int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ids := make(map[string]int64, len(targets))
	slugs := make([]string, 0, len(targets))

	for _, t := range targets {
		var id int64
		err := tx.QueryRow(ctx, `
			insert into services (slug, name, url, category, logo_url, active,
			                      poll_interval_seconds, active_interval_seconds, updated_at)
			values ($1, $2, $3, $4, $5, true, $6, $7, now())
			on conflict (slug) do update set
				name                    = excluded.name,
				url                     = excluded.url,
				category                = excluded.category,
				logo_url                = excluded.logo_url,
				active                  = true,
				poll_interval_seconds   = excluded.poll_interval_seconds,
				active_interval_seconds = excluded.active_interval_seconds,
				updated_at              = now()
			returning id`,
			t.Slug, t.Name, t.URL, nullify(t.Category), nullify(t.LogoURL),
			t.PollIntervalSeconds, t.ActiveIntervalSeconds,
		).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("sync: upsert %s: %w", t.Slug, err)
		}
		ids[t.Slug] = id
		slugs = append(slugs, t.Slug)
	}

	if len(slugs) > 0 {
		if _, err := tx.Exec(ctx, `
			update services set active = false, updated_at = now()
			where active and slug <> all($1)`, slugs,
		); err != nil {
			return nil, fmt.Errorf("sync: deactivate removed vendors: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sync: commit: %w", err)
	}
	return ids, nil
}

type ServiceRuntime struct {
	Slug         string
	Status       adapters.Health
	LastPolledAt *time.Time
}

func (s *Store) Runtime(ctx context.Context) (map[string]ServiceRuntime, error) {
	rows, err := s.pool.Query(ctx, `
		select s.slug,
		       coalesce(ss.status, 'unknown'),
		       s.last_polled_at
		from services s
		left join service_state ss on ss.service_id = s.id
		where s.active`)
	if err != nil {
		return nil, fmt.Errorf("runtime: query: %w", err)
	}
	defer rows.Close()

	out := map[string]ServiceRuntime{}
	for rows.Next() {
		var r ServiceRuntime
		var status string
		if err := rows.Scan(&r.Slug, &status, &r.LastPolledAt); err != nil {
			return nil, fmt.Errorf("runtime: scan: %w", err)
		}
		r.Status = adapters.Health(status)
		out[r.Slug] = r
	}
	return out, rows.Err()
}
