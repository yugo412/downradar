package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const KindPagerduty = "pagerduty"

const defaultPagerdutyEndpoint = "/api/"

// The incident history is read a month back, which is the span the page itself
// shows one page at a time. It is wide enough that a post resolved since the
// last poll is still in it, which is what closes the incident here.
const pagerdutyWindow = 30 * 24 * time.Hour

// The impact table PagerDuty status pages ship with. A page that words its own
// table differently leaves its components unknown rather than guessed, and the
// names turn up in the fetch log for whoever writes the mapping.
var pagerdutyImpact = map[string]Health{
	"operational":    HealthOK,
	"partial outage": HealthPartial,
	"outage":         HealthMajor,
	"maintenance":    HealthMaintenance,
}

// Pagerduty reads the JSON API a PagerDuty status page publishes at its origin:
// the component list, the services currently in trouble, the vocabulary the page
// names its states with, and the post history. The paths the page's own front end
// uses, such as /posts/dashboard, are not part of it, so every call goes to the
// origin of the url rather than to the url itself, and a vendor pinned to a page
// the reader was looking at still polls.
type Pagerduty struct {
	client *http.Client
}

func NewPagerduty(client *http.Client) *Pagerduty {
	if client == nil {
		client = DefaultClient()
	}
	return &Pagerduty{client: client}
}

func (p *Pagerduty) Kind() string { return KindPagerduty }

type pdService struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	// A page that stops showing a service marks it inactive, but the flag is
	// only read when it is there: a missing one is not the same as a service the
	// page has switched off.
	IsActive *bool `json:"is_active"`
}

type pdImpact struct {
	ServiceID string `json:"service_id"`
	Severity  string `json:"impact_severity_id"`
}

type pdEnum struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsFeatured bool   `json:"is_featured"`
}

type pdUpdate struct {
	StatusID   string `json:"status_id"`
	SeverityID string `json:"severity_id"`
	ReportedAt int64  `json:"reported_at"`
}

type pdPost struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	FirstUpdateAt int64    `json:"first_update_at"`
	LatestUpdate  pdUpdate `json:"latest_update"`
}

func (p *Pagerduty) Fetch(ctx context.Context, t Target) (Report, error) {
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultPagerdutyEndpoint
	}
	origin, err := pageOrigin(t.URL)
	if err != nil {
		return Report{}, err
	}
	api := origin + "/" + strings.TrimLeft(endpoint, "/")

	impactsBody, status, err := p.get(ctx, api+"impacted_services")
	if err != nil {
		return Report{HTTPStatus: status, Raw: impactsBody}, err
	}
	var impacts struct {
		PageID   string     `json:"status_page_id"`
		Services []pdImpact `json:"impacted_services"`
	}
	if err := json.Unmarshal(impactsBody, &impacts); err != nil {
		return Report{HTTPStatus: status, Raw: impactsBody},
			fmt.Errorf("pagerduty: decode impacted services: %w", err)
	}
	if impacts.PageID == "" {
		return Report{HTTPStatus: status, Raw: impactsBody},
			fmt.Errorf("pagerduty: %s is not a PagerDuty status page", api+"impacted_services")
	}

	servicesBody, status, err := p.get(ctx, api+"services")
	if err != nil {
		return Report{HTTPStatus: status, Raw: servicesBody}, err
	}
	var services struct {
		Services []pdService `json:"services"`
	}
	if err := json.Unmarshal(servicesBody, &services); err != nil {
		return Report{HTTPStatus: status, Raw: servicesBody},
			fmt.Errorf("pagerduty: decode services: %w", err)
	}

	rep := Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        servicesBody,
		PageID:     impacts.PageID,
	}

	enumsBody, _, err := p.get(ctx, api+"post_enums")
	if err != nil {
		return rep, err
	}
	var enums struct {
		Enums []pdEnum `json:"post_enums"`
	}
	if err := json.Unmarshal(enumsBody, &enums); err != nil {
		return rep, fmt.Errorf("pagerduty: decode post enums: %w", err)
	}
	enumsByID := make(map[string]pdEnum, len(enums.Enums))
	for _, e := range enums.Enums {
		enumsByID[e.ID] = e
	}

	since := rep.FetchedAt.Add(-pagerdutyWindow).UnixMilli()
	until := rep.FetchedAt.UnixMilli()
	postsBody, _, err := p.get(ctx, api+"posts?since="+strconv.FormatInt(since, 10)+
		"&until="+strconv.FormatInt(until, 10))
	if err != nil {
		return rep, err
	}
	var posts struct {
		Posts []pdPost `json:"posts"`
	}
	if err := json.Unmarshal(postsBody, &posts); err != nil {
		return rep, fmt.Errorf("pagerduty: decode posts: %w", err)
	}

	impacted := make(map[string]Health, len(impacts.Services))
	for _, i := range impacts.Services {
		impacted[i.ServiceID] = pagerdutyImpactHealth(enumsByID[i.Severity])
	}

	include := lowerSet(t.Include)
	statuses := make([]Health, 0, len(services.Services))
	for i, s := range services.Services {
		if s.IsActive != nil && !*s.IsActive {
			continue
		}
		name := s.DisplayName
		if name == "" {
			name = s.Name
		}
		if name == "" {
			continue
		}
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}

		health := HealthOK
		if down, ok := impacted[s.ID]; ok {
			health = down
		}
		rep.Components = append(rep.Components, ComponentStatus{
			SourceID: s.ID,
			Name:     name,
			Position: i + 1,
			Status:   health,
		})
		statuses = append(statuses, health)
	}

	if len(statuses) > 0 {
		rep.Global = Worst(statuses...)
	} else {
		rep.Global = HealthUnknown
	}

	for _, post := range posts.Posts {
		if post.ID == "" || post.Title == "" {
			continue
		}
		state, known := enumsByID[post.LatestUpdate.StatusID]
		item := Incident{
			SourceID:  post.ID,
			Name:      post.Title,
			Status:    state.Name,
			Impact:    enumsByID[post.LatestUpdate.SeverityID].Name,
			StartedAt: pagerdutyTime(post.FirstUpdateAt),
			URL:       origin + "/posts/details/" + post.ID,
		}
		// The page keeps a post open while its latest update names a state the
		// page is showing; the one it stops showing, resolved or completed,
		// dates the end.
		if known && !state.IsFeatured {
			resolved := pagerdutyTime(post.LatestUpdate.ReportedAt)
			item.ResolvedAt = &resolved
		}
		rep.Incidents = append(rep.Incidents, item)
	}

	return rep, nil
}

func pagerdutyImpactHealth(enum pdEnum) Health {
	if health, ok := pagerdutyImpact[strings.ToLower(strings.TrimSpace(enum.Name))]; ok {
		return health
	}
	return HealthUnknown
}

func pagerdutyTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func pageOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("pagerduty: parse url %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("pagerduty: url %q has no host", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func (p *Pagerduty) get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("pagerduty: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("pagerduty: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("pagerduty: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, fmt.Errorf("pagerduty: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func init() {
	Register(NewPagerduty(nil))
}
