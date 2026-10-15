package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const KindStatuspage = "statuspage"

const defaultStatuspageEndpoint = "/api/v2/summary.json"

var statuspageIndicator = map[string]Health{
	"none":        HealthOK,
	"minor":       HealthDegraded,
	"major":       HealthPartial,
	"critical":    HealthMajor,
	"maintenance": HealthMaintenance,
}

var statuspageComponent = map[string]Health{
	"operational":          HealthOK,
	"degraded_performance": HealthDegraded,
	"partial_outage":       HealthPartial,
	"major_outage":         HealthMajor,
	"under_maintenance":    HealthMaintenance,
}

type Statuspage struct {
	client *http.Client
}

func NewStatuspage(client *http.Client) *Statuspage {
	if client == nil {
		client = DefaultClient()
	}
	return &Statuspage{client: client}
}

func DefaultClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
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

func (s *Statuspage) Fetch(ctx context.Context, t Target) (Report, error) {
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultStatuspageEndpoint
	}
	url := strings.TrimRight(t.URL, "/") + "/" + strings.TrimLeft(endpoint, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Report{}, fmt.Errorf("statuspage: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return Report{}, fmt.Errorf("statuspage: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Report{}, fmt.Errorf("statuspage: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("statuspage: %s returned HTTP %d", url, resp.StatusCode)
	}

	var sum spSummary
	if err := json.Unmarshal(body, &sum); err != nil {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("statuspage: decode json: %w", err)
	}
	if sum.Page.ID == "" {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("statuspage: %s is not a statuspage (empty page.id)", url)
	}

	rep := Report{
		FetchedAt:       time.Now().UTC(),
		SourceUpdatedAt: sum.Page.UpdatedAt.UTC(),
		PageID:          sum.Page.ID,
		HTTPStatus:      resp.StatusCode,
		Raw:             body,
	}

	include := lowerSet(t.Include)

	var statuses []Health
	for _, c := range sum.Components {
		if c.Group {
			continue
		}
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(c.Name))] {
			continue
		}
		health, ok := statuspageComponent[c.Status]
		if !ok {
			health = HealthUnknown
		}
		rep.Components = append(rep.Components, ComponentStatus{
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
		global = HealthUnknown
	}
	if global == HealthUnknown && len(statuses) > 0 {
		global = Worst(statuses...)
	}
	rep.Global = global

	for _, inc := range sum.Incidents {
		rep.Incidents = append(rep.Incidents, Incident{
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

func lowerSet(in []string) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[strings.ToLower(strings.TrimSpace(s))] = true
	}
	return out
}

func init() {
	Register(NewStatuspage(nil))
}
