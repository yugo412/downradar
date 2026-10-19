package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("store: not found")

type VendorSummary struct {
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	URL          string     `json:"url"`
	Category     string     `json:"category,omitempty"`
	LogoURL      string     `json:"logoUrl,omitempty"`
	Status       string     `json:"status"`
	ChangedAt    *time.Time `json:"changedAt,omitempty"`
	Components   int        `json:"components"`
	Degraded     int        `json:"degradedComponents"`
	LastPolledAt *time.Time `json:"lastPolledAt,omitempty"`
}

type ComponentView struct {
	SourceID  string     `json:"sourceId"`
	Name      string     `json:"name"`
	Group     string     `json:"group,omitempty"`
	Position  int        `json:"position"`
	Status    string     `json:"status"`
	ChangedAt *time.Time `json:"changedAt,omitempty"`
}

type EventView struct {
	Status    string    `json:"status"`
	Component string    `json:"component,omitempty"`
	ChangedAt time.Time `json:"changedAt"`
}

type IncidentView struct {
	SourceID   string     `json:"sourceId"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Impact     string     `json:"impact,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
	URL        string     `json:"url,omitempty"`
}

type VendorDetail struct {
	VendorSummary
	Aliases        []string        `json:"aliases,omitempty"`
	ComponentsList []ComponentView `json:"componentsList"`
	RecentEvents   []EventView     `json:"recentEvents"`
	Incidents      []IncidentView  `json:"incidents"`
	Subscribers    int             `json:"subscribers"`
}

func (s *Store) ListVendors(ctx context.Context) ([]VendorSummary, error) {
	rows, err := s.pool.Query(ctx, `
		select s.slug,
		       s.name,
		       coalesce(s.url, ''),
		       coalesce(s.category, ''),
		       coalesce(s.logo_url, ''),
		       coalesce(ss.status, 'unknown'),
		       ss.changed_at,
		       s.last_polled_at,
		       coalesce(agg.total, 0),
		       coalesce(agg.degraded, 0)
		from services s
		left join service_state ss on ss.service_id = s.id
		left join lateral (
			select count(*) as total,
			       count(*) filter (where cs.status <> 'ok') as degraded
			from components c
			join component_state cs on cs.component_id = c.id
			where c.service_id = s.id and c.active
		) agg on true
		where s.active and s.status = 'published' and s.owner_id is null
		order by s.name`)
	if err != nil {
		return nil, fmt.Errorf("read: list vendors: %w", err)
	}
	defer rows.Close()

	return scanVendorSummaries(rows)
}

func scanVendorSummaries(rows pgx.Rows) ([]VendorSummary, error) {
	var out []VendorSummary
	for rows.Next() {
		var v VendorSummary
		if err := rows.Scan(&v.Slug, &v.Name, &v.URL, &v.Category, &v.LogoURL, &v.Status,
			&v.ChangedAt, &v.LastPolledAt, &v.Components, &v.Degraded); err != nil {
			return nil, fmt.Errorf("read: scan vendor: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// OwnedVendors is the public list shape, scoped to the private services of one
// person. The dashboard adds these to the catalogue for a signed in visitor, in
// the browser, so the cached page stays public.
func (s *Store) OwnedVendors(ctx context.Context, userID int64) ([]VendorSummary, error) {
	rows, err := s.pool.Query(ctx, `
		select s.slug,
		       s.name,
		       coalesce(s.url, ''),
		       coalesce(s.category, ''),
		       coalesce(s.logo_url, ''),
		       coalesce(ss.status, 'unknown'),
		       ss.changed_at,
		       s.last_polled_at,
		       coalesce(agg.total, 0),
		       coalesce(agg.degraded, 0)
		from services s
		left join service_state ss on ss.service_id = s.id
		left join lateral (
			select count(*) as total,
			       count(*) filter (where cs.status <> 'ok') as degraded
			from components c
			join component_state cs on cs.component_id = c.id
			where c.service_id = s.id and c.active
		) agg on true
		where s.active and s.status = 'published' and s.owner_id = $1
		order by s.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("read: owned vendors: %w", err)
	}
	defer rows.Close()

	return scanVendorSummaries(rows)
}

// GetVendor returns one vendor's detail. viewer decides which rows are visible:
// the public sees the catalogue, an owner sees their own private services, and
// staff see everything.
func (s *Store) GetVendor(ctx context.Context, slug string, viewer Viewer) (*VendorDetail, error) {
	var v VendorDetail
	var serviceID int64

	err := s.pool.QueryRow(ctx, `
		select s.id, s.slug, s.name, coalesce(s.url, ''), coalesce(s.category, ''), coalesce(s.logo_url, ''),
		       coalesce(ss.status, 'unknown'), ss.changed_at, s.last_polled_at
		from services s
		left join service_state ss on ss.service_id = s.id
		where s.slug = $1 and s.active and s.status = 'published'
		  and (s.owner_id is null
		       or ($2::bigint > 0 and s.owner_id = $2)
		       or $3::boolean)`, slug, viewer.UserID, viewer.IsStaff,
	).Scan(&serviceID, &v.Slug, &v.Name, &v.URL, &v.Category, &v.LogoURL, &v.Status, &v.ChangedAt, &v.LastPolledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read: get vendor %s: %w", slug, err)
	}

	v.ComponentsList = []ComponentView{}
	v.RecentEvents = []EventView{}
	v.Incidents = []IncidentView{}

	// Who is watching: one alert is one subscriber, whether it follows the whole
	// vendor or a single component of it, and an alert whose channels have all
	// been unsubscribed from is not watching any more. A paused one still is.
	if err := s.pool.QueryRow(ctx, `
		select count(distinct t.subscription_id)
		from alert_targets t
		where t.service_id = $1
		  and exists (
			select 1 from alert_channels c
			where c.subscription_id = t.subscription_id and c.unsubscribed_at is null
		  )`, serviceID).Scan(&v.Subscribers); err != nil {
		return nil, fmt.Errorf("read: subscribers %s: %w", slug, err)
	}

	rows, err := s.pool.Query(ctx, `
		select c.source_id, c.name, coalesce(c.group_name, ''), c.position,
		       coalesce(cs.status, 'unknown'), cs.changed_at
		from components c
		left join component_state cs on cs.component_id = c.id
		where c.service_id = $1 and c.active
		order by c.position, c.name`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("read: components %s: %w", slug, err)
	}
	for rows.Next() {
		var c ComponentView
		if err := rows.Scan(&c.SourceID, &c.Name, &c.Group, &c.Position, &c.Status, &c.ChangedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read: scan component: %w", err)
		}
		v.ComponentsList = append(v.ComponentsList, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	v.Components = len(v.ComponentsList)
	for _, c := range v.ComponentsList {
		if c.Status != "ok" {
			v.Degraded++
		}
	}

	eventRows, err := s.pool.Query(ctx, `
		select e.status, coalesce(c.name, ''), e.changed_at
		from status_events e
		left join components c on c.id = e.component_id
		where e.service_id = $1
		order by e.changed_at desc
		limit 30`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("read: events %s: %w", slug, err)
	}
	for eventRows.Next() {
		var e EventView
		if err := eventRows.Scan(&e.Status, &e.Component, &e.ChangedAt); err != nil {
			eventRows.Close()
			return nil, fmt.Errorf("read: scan event: %w", err)
		}
		v.RecentEvents = append(v.RecentEvents, e)
	}
	eventRows.Close()
	if err := eventRows.Err(); err != nil {
		return nil, err
	}

	incRows, err := s.pool.Query(ctx, `
		select source_id, name, status, coalesce(impact, ''), started_at, resolved_at, coalesce(url, '')
		from incidents
		where service_id = $1
		order by started_at desc nulls last
		limit 20`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("read: incidents %s: %w", slug, err)
	}
	for incRows.Next() {
		var i IncidentView
		if err := incRows.Scan(&i.SourceID, &i.Name, &i.Status, &i.Impact,
			&i.StartedAt, &i.ResolvedAt, &i.URL); err != nil {
			incRows.Close()
			return nil, fmt.Errorf("read: scan incident: %w", err)
		}
		v.Incidents = append(v.Incidents, i)
	}
	incRows.Close()
	return &v, incRows.Err()
}

func (s *Store) FindVendor(ctx context.Context, nameOrSlug string) (string, error) {
	var slug string
	err := s.pool.QueryRow(ctx, `
		select slug from services
		where active and owner_id is null and status <> 'rejected'
		  and (lower(slug) = lower($1) or lower(name) = lower($1))
		order by status desc
		limit 1`, strings.TrimSpace(nameOrSlug)).Scan(&slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read: find vendor %q: %w", nameOrSlug, err)
	}
	return slug, nil
}

type Stats struct {
	Total    int `json:"total"`
	Healthy  int `json:"healthy"`
	Degraded int `json:"degraded"`
	Down     int `json:"down"`
	Unknown  int `json:"unknown"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `
		select count(*),
		       count(*) filter (where coalesce(ss.status, 'unknown') in ('ok', 'maintenance')),
		       count(*) filter (where coalesce(ss.status, 'unknown') in ('degraded', 'partial')),
		       count(*) filter (where coalesce(ss.status, 'unknown') = 'major'),
		       count(*) filter (where coalesce(ss.status, 'unknown') = 'unknown')
		from services s
		left join service_state ss on ss.service_id = s.id
		where s.active and s.status = 'published' and s.owner_id is null`).Scan(&st.Total, &st.Healthy, &st.Degraded, &st.Down, &st.Unknown)
	if err != nil {
		return st, fmt.Errorf("read: stats: %w", err)
	}
	return st, nil
}
