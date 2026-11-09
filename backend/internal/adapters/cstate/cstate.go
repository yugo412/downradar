// Package cstate reads status pages built with cState. The generator publishes
// the page's state as JSON at /index.json, so the overall state, the systems and
// their open issues are read from there rather than from the rendered page.
package cstate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindCState = "cstate"

const defaultCStateEndpoint = "/index.json"

// cState has three states of its own: a system is ok, disrupted or down, and a
// notice is what it calls an informational post or a maintenance announcement.
// A disruption is an incident affecting part of the page, which is what the site
// calls a partial outage.
var cstateHealth = map[string]adapters.Health{
	"ok":        adapters.HealthOK,
	"disrupted": adapters.HealthPartial,
	"down":      adapters.HealthMajor,
	"notice":    adapters.HealthMaintenance,
}

// The severity of an issue carries the same meaning as the state of the system
// it affects, written as the impact the site shows beside an incident.
var cstateImpact = map[string]string{
	"down":      "major",
	"disrupted": "partial",
	"notice":    "maintenance",
}

type CState struct {
	client *http.Client
}

func NewCState(client *http.Client) *CState {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &CState{client: client}
}

func (c *CState) Kind() string { return KindCState }

type cstatePage struct {
	Is            string         `json:"is"`
	CStateVersion string         `json:"cStateVersion"`
	APIVersion    string         `json:"apiVersion"`
	Title         string         `json:"title"`
	SummaryStatus string         `json:"summaryStatus"`
	Systems       []cstateSystem `json:"systems"`
}

type cstateSystem struct {
	Name       string        `json:"name"`
	Category   string        `json:"category"`
	Status     string        `json:"status"`
	Unresolved []cstateIssue `json:"unresolvedIssues"`
}

// cstateIssue is one open issue as a system carries it. Only the issues attached
// to a system are read: one with no affected system is an announcement rather
// than an incident, though the page's own summary still counts it.
type cstateIssue struct {
	Title         string   `json:"title"`
	CreatedAt     string   `json:"createdAt"`
	LastMod       string   `json:"lastMod"`
	Permalink     string   `json:"permalink"`
	Severity      string   `json:"severity"`
	Resolved      bool     `json:"resolved"`
	Informational bool     `json:"informational"`
	ResolvedAt    string   `json:"resolvedAt"`
	Affected      []string `json:"affected"`
	Filename      string   `json:"filename"`
}

func (c *CState) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	base := strings.TrimRight(t.URL, "/")
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultCStateEndpoint
	}
	pageURL := base + "/" + strings.TrimLeft(endpoint, "/")

	body, status, retryAfter, err := c.get(ctx, pageURL)
	if err != nil {
		return adapters.Report{HTTPStatus: status, RetryAfter: retryAfter, Raw: body}, err
	}

	var page cstatePage
	if err := json.Unmarshal(body, &page); err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("cstate: decode %s: %w", pageURL, err)
	}
	if page.CStateVersion == "" {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("cstate: %s is not a cState page", pageURL)
	}
	if len(page.Systems) == 0 {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("cstate: %s lists no systems", pageURL)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        body,
		Global:     cstateHealthOf(page.SummaryStatus),
	}

	include := adapters.LowerSet(t.Include)
	var statuses []adapters.Health
	for i, system := range page.Systems {
		name := strings.TrimSpace(system.Name)
		if name == "" {
			continue
		}
		health := cstateHealthOf(system.Status)
		statuses = append(statuses, health)
		if len(include) > 0 && !include[strings.ToLower(name)] {
			continue
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: name,
			Name:     name,
			Group:    strings.TrimSpace(system.Category),
			Position: i + 1,
			Status:   health,
		})
	}

	// A page whose overall state did not arrive still says something per system.
	if rep.Global == adapters.HealthUnknown {
		rep.Global = adapters.Worst(statuses...)
	}

	pageBase, _ := url.Parse(t.URL)
	seen := make(map[string]bool)
	for _, system := range page.Systems {
		for _, issue := range system.Unresolved {
			id := cstateIssueID(issue)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			rep.Incidents = append(rep.Incidents, cstateIncident(issue, id, pageBase))
		}
	}

	return rep, nil
}

// cstateIssueID names an issue the same way every system that is affected by it
// does, so one outage is not listed once per system. The file name is stable
// across rebuilds, while the permalink is what a person can open.
func cstateIssueID(issue cstateIssue) string {
	if filename := strings.TrimSpace(issue.Filename); filename != "" {
		return filename
	}
	if permalink := strings.TrimSpace(issue.Permalink); permalink != "" {
		return permalink
	}
	return strings.TrimSpace(issue.Title)
}

func cstateIncident(issue cstateIssue, id string, base *url.URL) adapters.Incident {
	item := adapters.Incident{
		SourceID:  id,
		Name:      strings.TrimSpace(issue.Title),
		Status:    "investigating",
		Impact:    cstateImpact[strings.TrimSpace(strings.ToLower(issue.Severity))],
		StartedAt: cstateTime(issue.CreatedAt),
	}

	// An informational post is not under investigation; it is the page's way of
	// announcing maintenance or a notice, and the badge says so.
	if strings.EqualFold(strings.TrimSpace(issue.Severity), "notice") {
		item.Status = "notice"
	}

	if resolved := cstateTime(issue.ResolvedAt); !resolved.IsZero() {
		item.ResolvedAt = &resolved
	}
	if permalink := strings.TrimSpace(issue.Permalink); permalink != "" {
		item.URL = permalink
		if ref, err := url.Parse(permalink); err == nil && base != nil {
			item.URL = base.ResolveReference(ref).String()
		}
	}
	return item
}

// cstateTime reads what the generator writes for a date. A time printed by Go
// carries its zone twice when the zone has no short name (+0300 +0300), so the
// first twenty five characters are tried on their own before the whole value.
func cstateTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}

	if len(value) >= 25 {
		if at, err := time.Parse("2006-01-02 15:04:05 -0700", value[:25]); err == nil {
			return at.UTC()
		}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05",
		time.RFC3339,
	} {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC()
		}
	}
	return time.Time{}
}

func cstateHealthOf(state string) adapters.Health {
	if health, ok := cstateHealth[strings.TrimSpace(strings.ToLower(state))]; ok {
		return health
	}
	return adapters.HealthUnknown
}

func (c *CState) get(ctx context.Context, pageURL string) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("cstate: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("cstate: request %s: %w", pageURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, 0, fmt.Errorf("cstate: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, adapters.RetryAfter(resp),
			fmt.Errorf("cstate: %s returned HTTP %d", pageURL, resp.StatusCode)
	}
	return body, resp.StatusCode, 0, nil
}

func init() {
	adapters.Register(NewCState(nil))
}
