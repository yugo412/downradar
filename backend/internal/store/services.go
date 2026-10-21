package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

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

// Services returns every published vendor. The poller calls this once per
// cycle, which is what makes a row published by a moderator take effect without
// any reload mechanism. Proposals are not polled.
func (s *Store) Services(ctx context.Context) ([]ServiceRow, error) {
	rows, err := s.pool.Query(ctx, `
		select s.id, s.slug, s.name, coalesce(s.url, ''),
		       coalesce(s.category, ''), coalesce(s.logo_url, ''), coalesce(s.profile, ''),
		       coalesce(s.endpoint, ''), s.include_components, s.aliases,
		       s.allow_shared_page, s.poll_interval_seconds, s.active_interval_seconds,
		       coalesce(ss.status, 'unknown'), s.last_polled_at
		from services s
		left join service_state ss on ss.service_id = s.id
		where s.active and s.status = $1
		order by s.slug`, string(StatusPublished))
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

// ProposeService records a suggestion as a proposed row. It is what the suggest
// form writes; nothing polls it until a moderator publishes it. A signed in
// suggester is stored as the owner, which is attribution for the review queue,
// not a visibility choice: approving the suggestion clears it and publishes the
// row to the catalogue.
func (s *Store) ProposeService(ctx context.Context, name, pageURL string, ownerID *int64) (string, error) {
	slug, err := s.uniqueSlug(ctx, Slugify(name))
	if err != nil {
		return "", err
	}

	if _, err := s.pool.Exec(ctx, `
		insert into services (slug, name, url, status, active, owner_id, updated_at)
		values ($1, $2, $3, $4, true, $5, now())`,
		slug, name, nullify(pageURL), string(StatusProposed), ownerID,
	); err != nil {
		return "", fmt.Errorf("propose: insert %s: %w", slug, err)
	}
	return slug, nil
}

// uniqueSlug finds a slug that is not taken yet, so two proposals for the same
// name do not collide.
func (s *Store) uniqueSlug(ctx context.Context, base string) (string, error) {
	if base == "" {
		base = "vendor"
	}

	for i := 1; i <= 50; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}

		var exists bool
		if err := s.pool.QueryRow(ctx,
			`select exists (select 1 from services where slug = $1)`, candidate,
		).Scan(&exists); err != nil {
			return "", fmt.Errorf("slug: check %s: %w", candidate, err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("slug: no free slug for %q", base)
}

const maxSlugLength = 60

// Slugify turns a display name, or an address someone typed, into something
// usable in a url. Letters and digits are kept, everything else becomes a dash.
func Slugify(name string) string {
	var sb strings.Builder
	dashed := false

	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(r)
			dashed = false
		case sb.Len() > 0 && !dashed:
			sb.WriteByte('-')
			dashed = true
		}
		if sb.Len() >= maxSlugLength {
			break
		}
	}

	return strings.Trim(sb.String(), "-")
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
			                      poll_interval_seconds, active_interval_seconds, status, updated_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true, $11, $12, 'published', now())
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
				status                  = 'published',
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
