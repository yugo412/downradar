package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ServiceComponents returns every active component of a service, hidden ones
// included. Staff use it to decide what the public page shows; the public read
// (GetVendor) never returns a hidden row.
func (s *Store) ServiceComponents(ctx context.Context, slug string) ([]ComponentView, error) {
	var serviceID int64
	err := s.pool.QueryRow(ctx,
		`select id from services where slug = $1 and active`, slug,
	).Scan(&serviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: components service %s: %w", slug, err)
	}

	rows, err := s.pool.Query(ctx, `
		select c.source_id, c.name, coalesce(c.group_name, ''), c.position,
		       coalesce(cs.status, 'unknown'), cs.changed_at, c.hidden
		from components c
		left join component_state cs on cs.component_id = c.id
		where c.service_id = $1 and c.active
		order by c.position, c.name`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("store: components %s: %w", slug, err)
	}
	defer rows.Close()

	out := []ComponentView{}
	for rows.Next() {
		var c ComponentView
		if err := rows.Scan(&c.SourceID, &c.Name, &c.Group, &c.Position, &c.Status, &c.ChangedAt, &c.Hidden); err != nil {
			return nil, fmt.Errorf("store: scan component: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SetComponentHidden hides or shows one component on a service's public page.
// The row stays active and keeps being polled; only the public face changes.
func (s *Store) SetComponentHidden(ctx context.Context, slug, sourceID string, hidden bool) error {
	tag, err := s.pool.Exec(ctx, `
		update components c
		set hidden = $3, updated_at = now()
		from services s
		where c.service_id = s.id and s.slug = $1 and c.source_id = $2 and c.active`,
		slug, sourceID, hidden)
	if err != nil {
		return fmt.Errorf("store: hide component %s on %s: %w", sourceID, slug, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
