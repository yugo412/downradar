package store

import (
	"context"
	"fmt"
	"time"

	"downradar.info/backend/internal/adapters"
)

// ServiceRow is a vendor as stored in the table, together with the state the
// poller needs to decide whether it is due.
type ServiceRow struct {
	ID                    int64
	Slug                  string
	Name                  string
	URL                   string
	Category              string
	LogoURL               string
	Profile               string
	Endpoint              string
	Include               []string
	Aliases               []string
	AllowSharedPage       bool
	PollIntervalSeconds   int
	ActiveIntervalSeconds int
	Status                adapters.Health
	LastPolledAt          *time.Time
}

// Services returns every active vendor. The poller calls this once per cycle,
// which is what makes a row added from a form take effect without any reload
// mechanism.
func (s *Store) Services(ctx context.Context) ([]ServiceRow, error) {
	rows, err := s.pool.Query(ctx, `
		select s.id, s.slug, s.name, s.url,
		       coalesce(s.category, ''), coalesce(s.logo_url, ''), coalesce(s.profile, ''),
		       coalesce(s.endpoint, ''), s.include_components, s.aliases,
		       s.allow_shared_page, s.poll_interval_seconds, s.active_interval_seconds,
		       coalesce(ss.status, 'unknown'), s.last_polled_at
		from services s
		left join service_state ss on ss.service_id = s.id
		where s.active
		order by s.slug`)
	if err != nil {
		return nil, fmt.Errorf("services: query: %w", err)
	}
	defer rows.Close()

	var out []ServiceRow
	for rows.Next() {
		var r ServiceRow
		var status string
		if err := rows.Scan(
			&r.ID, &r.Slug, &r.Name, &r.URL,
			&r.Category, &r.LogoURL, &r.Profile,
			&r.Endpoint, &r.Include, &r.Aliases,
			&r.AllowSharedPage, &r.PollIntervalSeconds, &r.ActiveIntervalSeconds,
			&status, &r.LastPolledAt,
		); err != nil {
			return nil, fmt.Errorf("services: scan: %w", err)
		}
		r.Status = adapters.Health(status)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SeedServices upserts vendors from a bootstrap file.
//
// It deliberately does not deactivate anything: a vendor added through the form
// must survive a reseed, so the seed only ever writes what it was given.
func (s *Store) SeedServices(ctx context.Context, targets []adapters.Target) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("seed: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, t := range targets {
		// The array columns are NOT NULL, and an empty Go slice marshals to
		// NULL, so they are normalised here rather than at every call site.
		include := t.Include
		if include == nil {
			include = []string{}
		}
		aliases := t.Aliases
		if aliases == nil {
			aliases = []string{}
		}

		if _, err := tx.Exec(ctx, `
			insert into services (slug, name, url, category, logo_url, profile, endpoint,
			                      include_components, aliases, allow_shared_page, active,
			                      poll_interval_seconds, active_interval_seconds, updated_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true, $11, $12, now())
			on conflict (slug) do update set
				name                    = excluded.name,
				url                     = excluded.url,
				category                = excluded.category,
				logo_url                = excluded.logo_url,
				profile                 = excluded.profile,
				endpoint                = excluded.endpoint,
				include_components      = excluded.include_components,
				aliases                 = excluded.aliases,
				allow_shared_page       = excluded.allow_shared_page,
				active                  = true,
				poll_interval_seconds   = excluded.poll_interval_seconds,
				active_interval_seconds = excluded.active_interval_seconds,
				updated_at              = now()`,
			t.Slug, t.Name, t.URL, nullify(t.Category), nullify(t.LogoURL), t.Profile,
			nullify(t.Endpoint), include, aliases, t.AllowSharedPage,
			t.PollIntervalSeconds, t.ActiveIntervalSeconds,
		); err != nil {
			return 0, fmt.Errorf("seed: upsert %s: %w", t.Slug, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("seed: commit: %w", err)
	}
	return len(targets), nil
}
