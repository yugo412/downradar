// Package railway reads Railway's status page, which serves its components,
// incidents and uptime history as json.
package railway

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

const KindRailway = "railway"

const defaultRailwayEndpoint = "/api/status"

var railwayImpact = map[string]adapters.Health{
	"OPERATIONAL":          adapters.HealthOK,
	"DEGRADED_PERFORMANCE": adapters.HealthDegraded,
	"PARTIAL_OUTAGE":       adapters.HealthPartial,
	"MAJOR_OUTAGE":         adapters.HealthMajor,
	"MAINTENANCE":          adapters.HealthMaintenance,
}

type Railway struct {
	client *http.Client
}

func NewRailway(client *http.Client) *Railway {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Railway{client: client}
}

func (r *Railway) Kind() string { return KindRailway }

type railwayStatus struct {
	Entries []struct {
		Type string `json:"type"`
		Data struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Components []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"components"`
		} `json:"data"`
	} `json:"entries"`
	ActiveIncidents []railwayIncident `json:"activeIncidents"`
	RecentIncidents []railwayIncident `json:"recentIncidents"`
}

type railwayIncident struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvedAt *time.Time `json:"resolvedAt"`
	Components []struct {
		ID     string `json:"id"`
		Impact string `json:"impact"`
	} `json:"components"`
}

func (r *Railway) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultRailwayEndpoint
	}
	url := strings.TrimRight(t.URL, "/") + "/" + strings.TrimLeft(endpoint, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("railway: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := r.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("railway: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return adapters.Report{}, fmt.Errorf("railway: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("railway: %s returned HTTP %d", url, resp.StatusCode)
	}

	var payload railwayStatus
	if err := json.Unmarshal(body, &payload); err != nil {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("railway: decode json: %w", err)
	}
	if len(payload.Entries) == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("railway: %s lists no components", url)
	}

	// A component is only out of the ordinary when an incident says so: the
	// uptime history the page carries is not a current answer.
	incidentImpact := map[string]string{}
	for _, inc := range payload.ActiveIncidents {
		for _, c := range inc.Components {
			incidentImpact[c.ID] = c.Impact
		}
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        body,
	}

	include := adapters.LowerSet(t.Include)
	position := 0
	var statuses []adapters.Health
	for _, e := range payload.Entries {
		if e.Type != "group" {
			continue
		}
		for _, c := range e.Data.Components {
			position++
			if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(c.Name))] {
				continue
			}
			health := adapters.HealthOK
			if impact, ok := incidentImpact[c.ID]; ok {
				mapped, known := railwayImpact[impact]
				if known {
					health = mapped
				} else {
					health = adapters.HealthUnknown
				}
			}
			rep.Components = append(rep.Components, adapters.ComponentStatus{
				SourceID: c.ID,
				Name:     c.Name,
				Group:    e.Data.Name,
				Position: position,
				Status:   health,
			})
			statuses = append(statuses, health)
		}
	}

	rep.Global = adapters.HealthUnknown
	if len(statuses) > 0 {
		rep.Global = adapters.Worst(statuses...)
	}

	seen := map[string]bool{}
	for _, list := range [][]railwayIncident{payload.ActiveIncidents, payload.RecentIncidents} {
		for _, inc := range list {
			if seen[inc.ID] {
				continue
			}
			seen[inc.ID] = true
			rep.Incidents = append(rep.Incidents, adapters.Incident{
				SourceID:   inc.ID,
				Name:       inc.Title,
				Status:     inc.Status,
				Impact:     railwayIncidentImpact(inc),
				StartedAt:  inc.CreatedAt.UTC(),
				ResolvedAt: inc.ResolvedAt,
			})
		}
	}

	return rep, nil
}

// railwayIncidentImpact is the worst thing the incident said about the
// components it touched, which is what the page shows beside it.
func railwayIncidentImpact(inc railwayIncident) string {
	worst := ""
	severity := -1
	for _, c := range inc.Components {
		health := railwayImpact[c.Impact]
		if health.Severity() > severity {
			worst = c.Impact
			severity = health.Severity()
		}
	}
	return worst
}

func init() {
	adapters.Register(NewRailway(nil))
}
