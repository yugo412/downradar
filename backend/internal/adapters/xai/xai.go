// Package xai reads the status page of xAI. The page is a custom Next.js app on
// status.x.ai, and the data it shows lives on data.x.ai, so the adapter reads the
// JSON the page calls rather than the page itself. That also keeps it working
// when the page host refuses the poller.
package xai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindXAI = "xai"

// The status page and the data it reads are on different hosts. The endpoint is
// fixed because it is only ever xAI's page, which is why the profile is hidden
// from the default picker.
const xaiSummaryURL = "https://data.x.ai/status/summary.json"

// The page names its states in its own words. A component that is available is
// operational, disruption is the yellow degraded state, and outage is the red
// one. Info is informational rather than a fault, and no_data has nothing to say.
var xaiComponentState = map[string]adapters.Health{
	"available":  adapters.HealthOK,
	"info":       adapters.HealthOK,
	"disruption": adapters.HealthDegraded,
	"outage":     adapters.HealthMajor,
	"no_data":    adapters.HealthUnknown,
}

// The summary carries an indicator for the page as a whole. The page's own
// mapping folds minor and major into the same yellow state, and only a critical
// indicator is red.
var xaiIndicator = map[string]adapters.Health{
	"none":        adapters.HealthOK,
	"minor":       adapters.HealthDegraded,
	"major":       adapters.HealthDegraded,
	"critical":    adapters.HealthMajor,
	"maintenance": adapters.HealthMaintenance,
}

// An incident uses the same page vocabulary. The site's incident badge takes a
// word rather than a health state, so the page's impacts are mapped onto the
// words the rest of the site uses.
var xaiImpact = map[string]string{
	"minor":       "degraded",
	"major":       "degraded",
	"critical":    "major",
	"maintenance": "maintenance",
}

var xaiIncidentStatus = map[string]string{
	"investigating": "investigating",
	"identified":    "identified",
	"monitoring":    "monitoring",
	"resolved":      "resolved",
	"scheduled":     "scheduled",
	"in_progress":   "in_progress",
}

type XAI struct {
	client *http.Client
	url    string
}

func NewXAI(client *http.Client) *XAI {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &XAI{client: client, url: xaiSummaryURL}
}

func (x *XAI) Kind() string { return KindXAI }

type xaiSummary struct {
	Status struct {
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
	Components []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"components"`
	Incidents []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Impact     string `json:"impact"`
		CreatedAt  string `json:"created_at"`
		ResolvedAt string `json:"resolved_at"`
	} `json:"incidents"`
}

func (x *XAI) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	url := x.url
	if url == "" {
		url = xaiSummaryURL
	}
	body, status, retryAfter, err := x.get(ctx, url)
	if err != nil {
		return adapters.Report{HTTPStatus: status, RetryAfter: retryAfter, Raw: body}, err
	}

	var summary xaiSummary
	if err := json.Unmarshal(body, &summary); err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("xai: decode summary: %w", err)
	}
	if summary.Status.Indicator == "" {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("xai: the summary has no status indicator")
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        body,
		Global:     xaiHealth(summary.Status.Indicator),
	}

	var statuses []adapters.Health
	for position, component := range summary.Components {
		if component.ID == "" || component.Name == "" {
			continue
		}
		health := xaiComponentHealth(component.Status)
		statuses = append(statuses, health)
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: component.ID,
			Name:     component.Name,
			Position: position + 1,
			Status:   health,
		})
	}

	if rep.Global == adapters.HealthUnknown && len(statuses) > 0 {
		rep.Global = adapters.Worst(statuses...)
	}

	for _, incident := range summary.Incidents {
		if incident.ID == "" {
			continue
		}
		item := adapters.Incident{
			SourceID: incident.ID,
			Name:     strings.TrimSpace(incident.Name),
			Status:   xaiIncidentStatusOf(incident.Status),
			Impact:   xaiImpactOf(incident.Impact),
		}
		if at, err := time.Parse(time.RFC3339, incident.CreatedAt); err == nil {
			item.StartedAt = at.UTC()
		}
		if incident.ResolvedAt != "" {
			if at, err := time.Parse(time.RFC3339, incident.ResolvedAt); err == nil {
				resolved := at.UTC()
				item.ResolvedAt = &resolved
			}
		}
		rep.Incidents = append(rep.Incidents, item)
	}

	return rep, nil
}

func xaiHealth(indicator string) adapters.Health {
	if health, ok := xaiIndicator[strings.ToLower(strings.TrimSpace(indicator))]; ok {
		return health
	}
	return adapters.HealthUnknown
}

func xaiComponentHealth(state string) adapters.Health {
	if health, ok := xaiComponentState[strings.ToLower(strings.TrimSpace(state))]; ok {
		return health
	}
	return adapters.HealthUnknown
}

func xaiIncidentStatusOf(status string) string {
	key := strings.ToLower(strings.TrimSpace(status))
	if mapped, ok := xaiIncidentStatus[key]; ok {
		return mapped
	}
	return key
}

func xaiImpactOf(impact string) string {
	return xaiImpact[strings.ToLower(strings.TrimSpace(impact))]
}

func (x *XAI) get(ctx context.Context, url string) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("xai: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := x.client.Do(req)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("xai: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, 0, fmt.Errorf("xai: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, adapters.RetryAfter(resp),
			fmt.Errorf("xai: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, 0, nil
}

func init() {
	adapters.Register(NewXAI(nil))
}
