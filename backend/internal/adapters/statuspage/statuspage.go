package statuspage

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

const KindStatuspage = "statuspage"

const defaultStatuspageEndpoint = "/api/v2/summary.json"

var statuspageIndicator = map[string]adapters.Health{
	"none":        adapters.HealthOK,
	"minor":       adapters.HealthDegraded,
	"major":       adapters.HealthPartial,
	"critical":    adapters.HealthMajor,
	"maintenance": adapters.HealthMaintenance,
}

var statuspageComponent = map[string]adapters.Health{
	"operational":          adapters.HealthOK,
	"degraded_performance": adapters.HealthDegraded,
	"partial_outage":       adapters.HealthPartial,
	"major_outage":         adapters.HealthMajor,
	"under_maintenance":    adapters.HealthMaintenance,
}

type Statuspage struct {
	client *http.Client
}

func NewStatuspage(client *http.Client) *Statuspage {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Statuspage{client: client}
}

func (s *Statuspage) Kind() string { return KindStatuspage }

type spSummary struct {
	Page struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		URL       string    `json:"url"`
		UpdatedAt time.Time `json:"updated_at"`
	} `json:"page"`
	Status struct {
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
	Components []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Status   string `json:"status"`
		Position int    `json:"position"`
		GroupID  string `json:"group_id"`
		Group    bool   `json:"group"`
	} `json:"components"`
	Incidents []struct {
		ID         string     `json:"id"`
		Name       string     `json:"name"`
		Status     string     `json:"status"`
		Impact     string     `json:"impact"`
		CreatedAt  time.Time  `json:"created_at"`
		ResolvedAt *time.Time `json:"resolved_at"`
		Shortlink  string     `json:"shortlink"`
	} `json:"incidents"`
}

func (s *Statuspage) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultStatuspageEndpoint
	}
	url := strings.TrimRight(t.URL, "/") + "/" + strings.TrimLeft(endpoint, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("statuspage: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("statuspage: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return adapters.Report{}, fmt.Errorf("statuspage: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.Report{HTTPStatus: resp.StatusCode, RetryAfter: adapters.RetryAfter(resp), Raw: body},
			fmt.Errorf("statuspage: %s returned HTTP %d", url, resp.StatusCode)
	}

	var sum spSummary
	if err := json.Unmarshal(body, &sum); err != nil {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("statuspage: decode json: %w", err)
	}
	if sum.Page.ID == "" {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("statuspage: %s is not a statuspage (empty page.id)", url)
	}

	rep := adapters.Report{
		FetchedAt:       time.Now().UTC(),
		SourceUpdatedAt: sum.Page.UpdatedAt.UTC(),
		PageID:          sum.Page.ID,
		HTTPStatus:      resp.StatusCode,
		Raw:             body,
	}

	include := adapters.LowerSet(t.Include)

	var statuses []adapters.Health
	for _, c := range sum.Components {
		if c.Group {
			continue
		}
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(c.Name))] {
			continue
		}
		health, ok := statuspageComponent[c.Status]
		if !ok {
			health = adapters.HealthUnknown
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: c.ID,
			Name:     c.Name,
			Group:    c.GroupID,
			Position: c.Position,
			Status:   health,
		})
		statuses = append(statuses, health)
	}

	global, ok := statuspageIndicator[sum.Status.Indicator]
	if !ok {
		global = adapters.HealthUnknown
	}
	if global == adapters.HealthUnknown && len(statuses) > 0 {
		global = adapters.Worst(statuses...)
	}
	rep.Global = global

	for _, inc := range sum.Incidents {
		rep.Incidents = append(rep.Incidents, adapters.Incident{
			SourceID:   inc.ID,
			Name:       inc.Name,
			Status:     inc.Status,
			Impact:     inc.Impact,
			StartedAt:  inc.CreatedAt.UTC(),
			ResolvedAt: inc.ResolvedAt,
			URL:        inc.Shortlink,
		})
	}

	return rep, nil
}

func init() {
	adapters.Register(NewStatuspage(nil))
}
