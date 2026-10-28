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

// ErrSlugTaken is a service asked to answer to an address that another row
// already has. A slug someone typed is refused rather than numbered: they meant
// that address, not the next free one.
var ErrSlugTaken = errors.New("store: slug taken")

// ErrNoBotToken is a chat that has no bot to speak to it. A way that could never
// be sent to is refused rather than saved and left looking set up.
var ErrNoBotToken = errors.New("store: no bot for the chat")

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
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	Previous  string    `json:"previousStatus,omitempty"`
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

// GeneratedSummary is the short, machine-written description kept beside the
// official state. InputHash is how the writer knows the state it summarised is
// still the state on the page; neither it nor the model is part of the public
// answer.
type GeneratedSummary struct {
	Text        string    `json:"text"`
	GeneratedAt time.Time `json:"generatedAt"`
	Model       string    `json:"-"`
	InputHash   string    `json:"-"`
}

type VendorDetail struct {
	VendorSummary
	Aliases          []string          `json:"aliases,omitempty"`
	ComponentsList   []ComponentView   `json:"componentsList"`
	RecentEvents     []EventView       `json:"recentEvents"`
	Incidents        []IncidentView    `json:"incidents"`
	GeneratedSummary *GeneratedSummary `json:"generatedSummary,omitempty"`
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
		where s.active and s.status = 'published' and s.visibility = 'public'
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

// OwnedVendors is the public list shape, scoped to the published services of one
// person. The dashboard adds them for a signed in visitor in the browser, so the
// cached page stays public.
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
	var summaryText, summaryModel, summaryHash string
	var summaryAt *time.Time

	err := s.pool.QueryRow(ctx, `
		select s.id, s.slug, s.name, coalesce(s.url, ''), coalesce(s.category, ''), coalesce(s.logo_url, ''),
		       coalesce(ss.status, 'unknown'), ss.changed_at, s.last_polled_at,
		       coalesce(sm.summary, ''), sm.generated_at,
		       coalesce(sm.model, ''), coalesce(sm.input_hash, '')
		from services s
		left join service_state ss on ss.service_id = s.id
		left join service_summaries sm on sm.service_id = s.id
		where s.slug = $1 and s.active and s.status = 'published'
		  and (s.visibility = 'public'
		       or ($2::bigint > 0 and s.owner_id = $2)
		       or $3::boolean)`, slug, viewer.UserID, viewer.IsStaff,
	).Scan(&serviceID, &v.Slug, &v.Name, &v.URL, &v.Category, &v.LogoURL, &v.Status, &v.ChangedAt, &v.LastPolledAt,
		&summaryText, &summaryAt, &summaryModel, &summaryHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read: get vendor %s: %w", slug, err)
	}
	if summaryText != "" && summaryAt != nil && s.summariesEnabled {
		v.GeneratedSummary = &GeneratedSummary{
			Text:        summaryText,
			GeneratedAt: *summaryAt,
			Model:       summaryModel,
			InputHash:   summaryHash,
		}
	}

	v.ComponentsList = []ComponentView{}
	v.RecentEvents = []EventView{}
	v.Incidents = []IncidentView{}

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
		select e.id, e.status, coalesce(e.previous_status, ''), coalesce(c.name, ''), e.changed_at
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
		if err := eventRows.Scan(&e.ID, &e.Status, &e.Previous, &e.Component, &e.ChangedAt); err != nil {
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
	if err := incRows.Err(); err != nil {
		return nil, err
	}

	// A recovery is stored and its summary is removed by the worker a moment
	// later. Do not let that small gap put a sentence about an outage above a
	// vendor that the official data now calls healthy.
	if v.GeneratedSummary != nil && v.Status == "ok" && v.Degraded == 0 {
		open := false
		for _, i := range v.Incidents {
			if i.ResolvedAt == nil {
				open = true
				break
			}
		}
		if !open {
			v.GeneratedSummary = nil
		}
	}
	return &v, nil
}

func (s *Store) FindVendor(ctx context.Context, nameOrSlug string) (string, error) {
	var slug string
	err := s.pool.QueryRow(ctx, `
		select slug from services
		where active and visibility = 'public' and status <> 'rejected'
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

// ChangeView is one status change anywhere in the catalogue: what moved, from
// where to where, and when. Its id is the event's own, which identifies a feed
// item, and the vendor travels with it since a site-wide feed needs its name.
type ChangeView struct {
	ID         int64     `json:"id"`
	VendorSlug string    `json:"vendorSlug"`
	VendorName string    `json:"vendorName"`
	Component  string    `json:"component,omitempty"`
	From       string    `json:"from,omitempty"`
	To         string    `json:"to"`
	ChangedAt  time.Time `json:"changedAt"`
}

// RecentChanges is the newest changes across the catalogue, newest first, for the
// site-wide feed. Only public, unowned vendors count, and only real changes: a
// first sighting has nothing before it, so it is not news anybody was told.
func (s *Store) RecentChanges(ctx context.Context, limit int) ([]ChangeView, error) {
	rows, err := s.pool.Query(ctx, `
		select e.id, v.slug, v.name, coalesce(c.name, ''),
		       coalesce(e.previous_status, ''), e.status, e.changed_at
		from status_events e
		join services v on v.id = e.service_id
		left join components c on c.id = e.component_id
		where v.active and v.status = 'published' and v.owner_id is null
		  and e.previous_status is not null
		order by e.changed_at desc, e.id desc
		limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read: recent changes: %w", err)
	}
	defer rows.Close()

	var out []ChangeView
	for rows.Next() {
		var c ChangeView
		if err := rows.Scan(&c.ID, &c.VendorSlug, &c.VendorName, &c.Component,
			&c.From, &c.To, &c.ChangedAt); err != nil {
			return nil, fmt.Errorf("read: scan change: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
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
		where s.active and s.status = 'published' and s.visibility = 'public'`).Scan(&st.Total, &st.Healthy, &st.Degraded, &st.Down, &st.Unknown)
	if err != nil {
		return st, fmt.Errorf("read: stats: %w", err)
	}
	return st, nil
}
