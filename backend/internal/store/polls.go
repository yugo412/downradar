package store

import (
	"context"
	"fmt"
	"time"
)

// PollRunView is one attempt at fetching a vendor's page, whether it worked or
// not. That is what makes it a log of fetches rather than a log of changes: a
// page that has been refusing to answer is visible here and nowhere else.
type PollRunView struct {
	ID              int64     `json:"id"`
	OK              bool      `json:"ok"`
	HTTPStatus      *int      `json:"httpStatus,omitempty"`
	ComponentsFound int       `json:"componentsFound"`
	UnknownStatuses []string  `json:"unknownStatuses,omitempty"`
	Error           string    `json:"error,omitempty"`
	DurationMS      *int      `json:"durationMs,omitempty"`
	FetchedAt       time.Time `json:"fetchedAt"`
}

// RecentPollRuns returns the newest fetches of one service. The caller decides
// how far back to look, and the index on (service_id, created_at desc) keeps
// that cheap.
func (s *Store) RecentPollRuns(ctx context.Context, slug string, limit int) ([]PollRunView, error) {
	rows, err := s.pool.Query(ctx, `
		select r.id, r.ok, r.http_status, r.components_found, r.unknown_statuses,
		       coalesce(r.error, ''), r.duration_ms, r.created_at
		from poll_runs r
		join services s on s.id = r.service_id
		where s.slug = $1
		order by r.created_at desc, r.id desc
		limit $2`, slug, limit)
	if err != nil {
		return nil, fmt.Errorf("read: poll runs %s: %w", slug, err)
	}
	defer rows.Close()

	out := []PollRunView{}
	for rows.Next() {
		var r PollRunView
		if err := rows.Scan(&r.ID, &r.OK, &r.HTTPStatus, &r.ComponentsFound,
			&r.UnknownStatuses, &r.Error, &r.DurationMS, &r.FetchedAt); err != nil {
			return nil, fmt.Errorf("read: scan poll run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
