package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminService is a vendor as the management screens see it: every column that
// can be edited, plus the state needed to decide what the row may do next.
type AdminService struct {
	ID                    int64      `json:"id"`
	Slug                  string     `json:"slug"`
	Name                  string     `json:"name"`
	URL                   string     `json:"url"`
	Category              string     `json:"category,omitempty"`
	LogoURL               string     `json:"logoUrl,omitempty"`
	Profile               string     `json:"profile"`
	Endpoint              string     `json:"endpoint,omitempty"`
	Aliases               []string   `json:"aliases"`
	AllowSharedPage       bool       `json:"allowSharedPage"`
	PollIntervalSeconds   int        `json:"pollIntervalSeconds"`
	ActiveIntervalSeconds int        `json:"activeIntervalSeconds"`
	Status                string     `json:"status"`
	State                 string     `json:"state"`
	Active                bool       `json:"active"`
	Paused                bool       `json:"paused"`
	Visibility            string     `json:"visibility"`
	OwnerID               *int64     `json:"ownerId,omitempty"`
	OwnerName             string     `json:"ownerName,omitempty"`
	OwnerEmail            string     `json:"ownerEmail,omitempty"`
	LastPolledAt          *time.Time `json:"lastPolledAt,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	Components            int        `json:"components"`
	HasHistory            bool       `json:"hasHistory"`
}

const adminServiceColumns = `
	s.id, s.slug, s.name, coalesce(s.url, ''), coalesce(s.category, ''), coalesce(s.logo_url, ''),
	coalesce(s.profile, ''), coalesce(s.endpoint, ''), s.aliases, s.allow_shared_page,
	s.poll_interval_seconds, s.active_interval_seconds, s.status, s.active, s.paused, s.visibility,
	coalesce(st.status, 'unknown'),
	s.last_polled_at, s.created_at, s.updated_at,
	s.owner_id, coalesce(u.name, ''), coalesce(u.email, ''),
	(select count(*) from components c where c.service_id = s.id),
	exists (select 1 from status_events e where e.service_id = s.id)`

const adminServiceFrom = `
	from services s
	left join users u on u.id = s.owner_id
	left join service_state st on st.service_id = s.id`

func scanAdminService(row rowScanner) (*AdminService, error) {
	var a AdminService
	if err := row.Scan(
		&a.ID, &a.Slug, &a.Name, &a.URL, &a.Category, &a.LogoURL,
		&a.Profile, &a.Endpoint, &a.Aliases, &a.AllowSharedPage,
		&a.PollIntervalSeconds, &a.ActiveIntervalSeconds, &a.Status, &a.Active, &a.Paused, &a.Visibility,
		&a.State,
		&a.LastPolledAt, &a.CreatedAt, &a.UpdatedAt,
		&a.OwnerID, &a.OwnerName, &a.OwnerEmail,
		&a.Components, &a.HasHistory,
	); err != nil {
		return nil, err
	}
	if a.Aliases == nil {
		a.Aliases = []string{}
	}
	return &a, nil
}

// AdminListServices returns rows for the management list. filter is all, proposed,
// published, rejected, no_match or removed (soft deleted, kept for history), and
// search matches name, slug and url. Staff see every row, others only their own.
func (s *Store) AdminListServices(ctx context.Context, filter, search string, viewer Viewer) ([]AdminService, error) {
	if filter == "" {
		filter = "all"
	}

	rows, err := s.pool.Query(ctx, `
		select `+adminServiceColumns+adminServiceFrom+`
		where ($1 = 'all'
		       or ($1 = 'removed' and not s.active)
		       or ($1 in ('proposed', 'no_match', 'published', 'rejected') and s.active and s.status = $1))
		  and ($2 = ''
		       or position(lower($2) in lower(s.name)) > 0
		       or position(lower($2) in lower(s.slug)) > 0
		       or position(lower($2) in lower(coalesce(s.url, ''))) > 0)
		  and ($4::boolean or s.owner_id = $3::bigint)
		order by
			case
				when s.active and s.status = 'proposed' then 0
				when s.active and s.status = 'no_match' then 1
				when s.active and s.status = 'published' then 2
				when s.active then 3
				else 4
			end,
			s.name`, filter, search, viewer.UserID, viewer.IsStaff)
	if err != nil {
		return nil, fmt.Errorf("services: admin list: %w", err)
	}
	defer rows.Close()

	var out []AdminService
	for rows.Next() {
		a, err := scanAdminService(rows)
		if err != nil {
			return nil, fmt.Errorf("services: admin scan: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// DistinctCategories lists the categories already in use, so the form can offer
// them as suggestions rather than making every editor retype them.
func (s *Store) DistinctCategories(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		select distinct category from services
		where category is not null and category <> ''
		order by category`)
	if err != nil {
		return nil, fmt.Errorf("services: categories: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			return nil, fmt.Errorf("services: scan category: %w", err)
		}
		out = append(out, category)
	}
	return out, rows.Err()
}

func (s *Store) AdminGetService(ctx context.Context, slug string) (*AdminService, error) {
	a, err := scanAdminService(s.pool.QueryRow(ctx, `
		select `+adminServiceColumns+adminServiceFrom+`
		where s.slug = $1`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("services: admin get %s: %w", slug, err)
	}
	return a, nil
}

type NewService struct {
	Name                  string
	URL                   string
	Profile               string
	Category              string
	LogoURL               string
	Endpoint              string
	Aliases               []string
	PollIntervalSeconds   int
	ActiveIntervalSeconds int
	// Status is empty for a vendor that is live. A vendor added without a
	// profile that nothing could read is stored as no_match instead.
	Status string
	// Visibility is who may see the row: the catalogue, or only its owner and
	// the staff. An empty value follows OwnerID, which is what every caller
	// before the column meant.
	Visibility string
	OwnerID    *int64
}

// A vendor saved before its profile is known has no cadence of its own yet, so
// it is given the same intervals the columns declare. They are replaced the
// moment a profile is written.
const (
	defaultPollIntervalSeconds   = 300
	defaultActiveIntervalSeconds = 60
)

// CreateService adds a vendor that is live straight away, which is why the url
// and profile are validated by the caller: a published row without a profile is
// silently skipped by the poller.
func (s *Store) CreateService(ctx context.Context, in NewService) (string, error) {
	slug, err := s.uniqueSlug(ctx, Slugify(in.Name))
	if err != nil {
		return "", err
	}

	aliases := in.Aliases
	if aliases == nil {
		aliases = []string{}
	}

	status := in.Status
	if status == "" {
		status = string(StatusPublished)
	}

	visibility := in.Visibility
	if visibility == "" {
		visibility = VisibilityPublic
		if in.OwnerID != nil {
			visibility = VisibilityPrivate
		}
	}

	pollSeconds := in.PollIntervalSeconds
	if pollSeconds <= 0 {
		pollSeconds = defaultPollIntervalSeconds
	}
	activeSeconds := in.ActiveIntervalSeconds
	if activeSeconds <= 0 {
		activeSeconds = defaultActiveIntervalSeconds
	}

	if _, err := s.pool.Exec(ctx, `
		insert into services (slug, name, url, category, logo_url, profile, endpoint,
		                      aliases, include_components, allow_shared_page, active,
		                      poll_interval_seconds, active_interval_seconds, status,
		                      visibility, owner_id, updated_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, '{}', false, true, $9, $10, $11, $12, $13, now())`,
		slug, in.Name, nullify(in.URL), nullify(in.Category), nullify(in.LogoURL),
		nullify(in.Profile), nullify(in.Endpoint), aliases,
		pollSeconds, activeSeconds, status, visibility, in.OwnerID,
	); err != nil {
		return "", fmt.Errorf("services: create %s: %w", slug, err)
	}
	return slug, nil
}

// ServicePatch carries only the fields the caller wants to change. A nil field
// is left alone; an empty string clears a nullable column.
type ServicePatch struct {
	Name *string
	// Slug moves the row to another address. A slug that is already taken is
	// refused with ErrSlugTaken rather than numbered.
	Slug     *string
	URL      *string
	Profile  *string
	Category *string
	LogoURL  *string
	Endpoint *string
	Aliases  *[]string
	Status   *string
	Active   *bool
	Paused   *bool
	// The intervals are declared by the profile, so they travel with it when the
	// profile changes. A nil field leaves the stored value alone.
	PollIntervalSeconds   *int
	ActiveIntervalSeconds *int
	// SetOwner has to be separate from OwnerID, because a nil OwnerID is a
	// meaningful value here: it is what moves a service back to the catalogue.
	SetOwner bool
	OwnerID  *int64
	// Visibility moves a row between the catalogue and one person's list
	// without touching whose it is.
	Visibility *string
}

func (p ServicePatch) Empty() bool {
	return p.Name == nil && p.Slug == nil && p.URL == nil && p.Profile == nil && p.Category == nil &&
		p.LogoURL == nil && p.Endpoint == nil && p.Aliases == nil && p.Status == nil &&
		p.Active == nil && p.Paused == nil && p.PollIntervalSeconds == nil && p.ActiveIntervalSeconds == nil &&
		!p.SetOwner && p.Visibility == nil
}

func (s *Store) UpdateService(ctx context.Context, slug string, p ServicePatch) error {
	tag, err := s.pool.Exec(ctx, `
		update services set
			name       = coalesce($2::text, name),
			url        = case when $3::text is null then url else nullif($3::text, '') end,
			profile    = case when $4::text is null then profile else nullif($4::text, '') end,
			category   = case when $5::text is null then category else nullif($5::text, '') end,
			logo_url   = case when $6::text is null then logo_url else nullif($6::text, '') end,
			endpoint   = case when $7::text is null then endpoint else nullif($7::text, '') end,
			aliases    = coalesce($8::text[], aliases),
			status     = coalesce($9::text, status),
			active     = coalesce($10::boolean, active),
			owner_id   = case when $11::boolean then $12::bigint else owner_id end,
			poll_interval_seconds   = coalesce($13::int, poll_interval_seconds),
			active_interval_seconds = coalesce($14::int, active_interval_seconds),
			visibility = coalesce($15::text, visibility),
			slug       = coalesce($16::text, slug),
			paused     = coalesce($17::boolean, paused),
			updated_at = now()
		where slug = $1`,
		slug, p.Name, p.URL, p.Profile, p.Category, p.LogoURL, p.Endpoint,
		p.Aliases, p.Status, p.Active, p.SetOwner, p.OwnerID,
		p.PollIntervalSeconds, p.ActiveIntervalSeconds, p.Visibility, p.Slug, p.Paused)
	if err != nil {
		// The unique index on the address is the only one this statement can
		// trip, and a taken address is a person's answer to correct, not a fault.
		if isUniqueViolation(err) {
			return ErrSlugTaken
		}
		return fmt.Errorf("services: update %s: %w", slug, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const (
	DeletePermanently = "deleted"
	DeleteDeactivated = "deactivated"
)

// DeleteService decides what "delete" means for this row. A proposal has never
// been polled, so it is removed outright. A vendor that is live has history
// worth keeping, so it is only deactivated and can be restored later.
func (s *Store) DeleteService(ctx context.Context, slug string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("services: delete begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	err = tx.QueryRow(ctx, `select status from services where slug = $1 for update`, slug).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("services: delete lookup %s: %w", slug, err)
	}

	if status == string(StatusProposed) {
		if _, err := deleteServiceRows(ctx, tx, slug); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("services: delete commit %s: %w", slug, err)
		}
		return DeletePermanently, nil
	}

	if _, err := tx.Exec(ctx, `
		update services set active = false, updated_at = now() where slug = $1`, slug); err != nil {
		return "", fmt.Errorf("services: deactivate %s: %w", slug, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("services: deactivate commit %s: %w", slug, err)
	}
	return DeleteDeactivated, nil
}

// deleteServiceRows takes a service away for good, and everything only ever about
// it goes through the foreign keys: components, events, poll runs, raw payloads
// and alerts. It returns how many rows went, so a missing slug is distinguishable.
func deleteServiceRows(ctx context.Context, tx pgx.Tx, slug string) (int64, error) {
	tag, err := tx.Exec(ctx, `delete from services where slug = $1`, slug)
	if err != nil {
		return 0, fmt.Errorf("services: delete %s: %w", slug, err)
	}
	return tag.RowsAffected(), nil
}

// PurgeService takes a row away for good. Everything hanging off it goes through
// the foreign keys: components, status events, poll runs, raw payloads and targets.
// A subscription only watching this vendor goes too, even somebody else's.
func (s *Store) PurgeService(ctx context.Context, slug string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("services: purge begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	removed, err := deleteServiceRows(ctx, tx, slug)
	if err != nil {
		return err
	}
	if removed == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("services: purge commit %s: %w", slug, err)
	}
	return nil
}
