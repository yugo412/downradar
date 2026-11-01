// Package instatus reads status pages hosted by Instatus. The page publishes a
// small JSON API under /v3, so the overall state, the components and the
// notices are read from there rather than from the rendered page.
package instatus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindInstatus = "instatus"

const defaultInstatusEndpoint = "/v3/"

// Instatus writes its states as one word, so they are folded before lookup.
// The page itself only distinguishes up and down, while a component carries the
// more precise state.
var instatusState = map[string]adapters.Health{
	"operational":         adapters.HealthOK,
	"degradedperformance": adapters.HealthDegraded,
	"partialoutage":       adapters.HealthPartial,
	"majoroutage":         adapters.HealthMajor,
	"undermaintenance":    adapters.HealthMaintenance,
	"up":                  adapters.HealthOK,
	"down":                adapters.HealthMajor,
}

// The impact of an incident is the vendor's own severity, mapped onto the
// words the site uses for a component that is not operational.
var instatusImpact = map[string]string{
	"MAJOROUTAGE":         "major",
	"PARTIALOUTAGE":       "partial",
	"DEGRADEDPERFORMANCE": "degraded",
	"MAINTENANCE":         "maintenance",
	"UNDERMAINTENANCE":    "maintenance",
}

// An incident and a maintenance are both a notice, and their states are read
// the same way. A maintenance that has not started is still scheduled.
var instatusStatus = map[string]string{
	"INVESTIGATING": "investigating",
	"IDENTIFIED":    "identified",
	"MONITORING":    "monitoring",
	"RESOLVED":      "resolved",
	"SCHEDULED":     "scheduled",
	"NOTSTARTEDYET": "scheduled",
	"INPROGRESS":    "in_progress",
	"COMPLETED":     "completed",
}

type Instatus struct {
	client *http.Client
}

func NewInstatus(client *http.Client) *Instatus {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Instatus{client: client}
}

func (i *Instatus) Kind() string { return KindInstatus }

type instatusSummary struct {
	Page struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Status string `json:"status"`
	} `json:"page"`
	ActiveIncidents    []instatusNotice `json:"activeIncidents"`
	ActiveMaintenances []instatusNotice `json:"activeMaintenances"`
	RecentlyResolved   []instatusNotice `json:"recentlyResolvedIncidents"`
}

type instatusNotice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Impact   string `json:"impact"`
	Started  string `json:"started"`
	Start    string `json:"start"`
	Resolved string `json:"resolved"`
	URL      string `json:"url"`
}

type instatusComponent struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Status   string              `json:"status"`
	Order    int                 `json:"order"`
	IsParent bool                `json:"isParent"`
	Children []instatusComponent `json:"children"`
}

func (i *Instatus) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	base := strings.TrimRight(t.URL, "/")
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultInstatusEndpoint
	}
	root := base + "/" + strings.TrimLeft(endpoint, "/")

	summaryBody, status, retryAfter, err := i.get(ctx, root+"summary.json")
	if err != nil {
		return adapters.Report{HTTPStatus: status, RetryAfter: retryAfter, Raw: summaryBody}, err
	}

	var summary instatusSummary
	if err := json.Unmarshal(summaryBody, &summary); err != nil {
		return adapters.Report{HTTPStatus: status, Raw: summaryBody},
			fmt.Errorf("instatus: decode summary: %w", err)
	}
	if summary.Page.Status == "" {
		return adapters.Report{HTTPStatus: status, Raw: summaryBody},
			fmt.Errorf("instatus: %s returned no page status", root+"summary.json")
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        summaryBody,
		Global:     instatusHealth(summary.Page.Status),
	}

	componentsBody, status, retryAfter, err := i.get(ctx, root+"components.json")
	if err != nil {
		rep.HTTPStatus = status
		rep.RetryAfter = retryAfter
		return rep, err
	}
	var payload struct {
		Components []instatusComponent `json:"components"`
	}
	if err := json.Unmarshal(componentsBody, &payload); err != nil {
		return rep, fmt.Errorf("instatus: decode components: %w", err)
	}
	sort.SliceStable(payload.Components, func(a, b int) bool {
		return payload.Components[a].Order < payload.Components[b].Order
	})

	include := adapters.LowerSet(t.Include)
	position := 0
	var statuses []adapters.Health

	add := func(c instatusComponent, group string) {
		if c.ID == "" || c.Name == "" {
			return
		}
		health := instatusHealth(c.Status)
		statuses = append(statuses, health)
		position++
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(c.Name))] {
			return
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: c.ID,
			Name:     c.Name,
			Group:    group,
			Position: position,
			Status:   health,
		})
	}

	// A parent is the heading of a group rather than a row of its own, and the
	// children below it are what the page lists. A parent with no children is
	// still shown, since dropping it would hide a real component. Both levels
	// are ordered by the page's own order, not by the order the API lists them.
	for _, c := range payload.Components {
		if c.IsParent && len(c.Children) > 0 {
			children := append([]instatusComponent(nil), c.Children...)
			sort.SliceStable(children, func(a, b int) bool {
				return children[a].Order < children[b].Order
			})
			for _, child := range children {
				add(child, c.Name)
			}
			continue
		}
		add(c, "")
	}

	if rep.Global == adapters.HealthUnknown && len(statuses) > 0 {
		rep.Global = adapters.Worst(statuses...)
	}

	page, _ := url.Parse(t.URL)
	for _, n := range summary.ActiveIncidents {
		rep.Incidents = append(rep.Incidents, instatusIncident(n, page, "investigating"))
	}
	for _, n := range summary.ActiveMaintenances {
		rep.Incidents = append(rep.Incidents, instatusIncident(n, page, "scheduled"))
	}
	for _, n := range summary.RecentlyResolved {
		rep.Incidents = append(rep.Incidents, instatusIncident(n, page, "resolved"))
	}

	return rep, nil
}

func instatusIncident(n instatusNotice, base *url.URL, fallback string) adapters.Incident {
	item := adapters.Incident{
		SourceID: n.ID,
		Name:     strings.TrimSpace(n.Name),
		Status:   instatusStatusOf(n.Status),
		Impact:   instatusImpactOf(n.Impact),
	}
	if item.Status == "" {
		item.Status = fallback
	}

	started := n.Started
	if started == "" {
		started = n.Start
	}
	if at, err := time.Parse(time.RFC3339, started); err == nil {
		item.StartedAt = at.UTC()
	}
	if n.Resolved != "" {
		if at, err := time.Parse(time.RFC3339, n.Resolved); err == nil {
			resolved := at.UTC()
			item.ResolvedAt = &resolved
		}
	}
	if n.URL != "" {
		item.URL = n.URL
		if ref, err := url.Parse(n.URL); err == nil && base != nil {
			item.URL = base.ResolveReference(ref).String()
		}
	}
	return item
}

func instatusHealth(state string) adapters.Health {
	if health, ok := instatusState[fold(state)]; ok {
		return health
	}
	return adapters.HealthUnknown
}

func instatusStatusOf(state string) string {
	if status, ok := instatusStatus[strings.TrimSpace(strings.ToUpper(state))]; ok {
		return status
	}
	if state == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(state))
}

func instatusImpactOf(impact string) string {
	return instatusImpact[strings.TrimSpace(strings.ToUpper(impact))]
}

// fold lowercases a state and drops the separators, so both the one word the
// API uses and a spaced spelling read the same.
func fold(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(value)
}

func (i *Instatus) get(ctx context.Context, url string) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("instatus: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := i.client.Do(req)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("instatus: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, 0, fmt.Errorf("instatus: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, adapters.RetryAfter(resp), fmt.Errorf("instatus: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, 0, nil
}

func init() {
	adapters.Register(NewInstatus(nil))
}
