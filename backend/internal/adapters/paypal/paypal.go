// Package paypal reads PayPal's status page, which serves the components and
// the events behind it as json.
package paypal

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

const KindPaypal = "paypal"

const (
	defaultComponentsEndpoint = "/api/v1/components"
	defaultEventsEndpoint     = "/api/v1/events?s=true"
)

var paypalStatus = map[string]adapters.Health{
	"operational":                adapters.HealthOK,
	"under maintenance":          adapters.HealthMaintenance,
	"maintenance":                adapters.HealthMaintenance,
	"degraded performance":       adapters.HealthDegraded,
	"service disruption":         adapters.HealthDegraded,
	"partial service disruption": adapters.HealthPartial,
	"service outage":             adapters.HealthMajor,
}

type Paypal struct {
	client *http.Client
}

func NewPaypal(client *http.Client) *Paypal {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Paypal{client: client}
}

func (p *Paypal) Kind() string { return KindPaypal }

type componentsPayload struct {
	Result []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		ParentName  string `json:"parentName"`
		BrandName   string `json:"brandName"`
		SortOrder   int    `json:"sortOrder"`
		Category    struct {
			Name string `json:"name"`
		} `json:"category"`
		Status struct {
			Production string `json:"production"`
		} `json:"status"`
	} `json:"result"`
}

type eventsPayload struct {
	Result []struct {
		ID       int64  `json:"id"`
		Summary  string `json:"summary"`
		Type     string `json:"type"`
		State    string `json:"state"`
		Severity string `json:"severity"`
		PubDate  string `json:"pubDate"`
		Start    string `json:"startDate"`
		End      string `json:"endDate"`
	} `json:"result"`
}

func (p *Paypal) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	componentsEndpoint := t.Endpoint
	if componentsEndpoint == "" {
		componentsEndpoint = defaultComponentsEndpoint
	}

	origin, err := pageOrigin(t.URL)
	if err != nil {
		return adapters.Report{}, err
	}
	componentsURL := strings.TrimRight(origin, "/") + "/" + strings.TrimLeft(componentsEndpoint, "/")
	body, status, err := p.get(ctx, componentsURL)
	if err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body}, err
	}

	var components componentsPayload
	if err := json.Unmarshal(body, &components); err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("paypal: decode %s: %w", componentsURL, err)
	}
	if len(components.Result) == 0 {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("paypal: %s lists no components", componentsURL)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        body,
	}

	include := adapters.LowerSet(t.Include)
	var statuses []adapters.Health
	for _, c := range components.Result {
		name := c.DisplayName
		if name == "" {
			name = c.Name
		}
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}
		// A component can exist for the sandbox only, and then it says
		// nothing about production.
		if strings.TrimSpace(c.Status.Production) == "" {
			continue
		}
		health, ok := paypalStatus[strings.ToLower(strings.TrimSpace(c.Status.Production))]
		if !ok {
			health = adapters.HealthUnknown
		}

		group := c.ParentName
		if group == "" {
			group = c.Category.Name
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: fmt.Sprintf("%d", c.ID),
			Name:     name,
			Group:    group,
			Position: c.SortOrder,
			Status:   health,
		})
		statuses = append(statuses, health)
	}

	rep.Global = adapters.HealthUnknown
	if len(statuses) > 0 {
		rep.Global = adapters.Worst(statuses...)
	}

	// The events are what the page lists under the components. A failed read
	// leaves the status alone: what was already read is still true.
	eventsURL := strings.TrimRight(origin, "/") + "/" + strings.TrimLeft(defaultEventsEndpoint, "/")
	if events, _, err := p.get(ctx, eventsURL); err == nil {
		rep.Incidents = paypalIncidents(events)
	}

	return rep, nil
}

// pageOrigin drops the path: the api sits at the top of the host, while the
// page a reader is sent to can be a deep link.
func pageOrigin(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("paypal: parse %s: %w", rawURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("paypal: %s carries no host", rawURL)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func paypalIncidents(body []byte) []adapters.Incident {
	var events eventsPayload
	if err := json.Unmarshal(body, &events); err != nil {
		return nil
	}

	out := make([]adapters.Incident, 0, len(events.Result))
	for _, e := range events.Result {
		started, err := time.Parse(time.RFC3339, e.Start)
		if err != nil {
			started, _ = time.Parse(time.RFC3339, e.PubDate)
		}
		var resolved *time.Time
		if strings.EqualFold(e.State, "closed") {
			if end, err := time.Parse(time.RFC3339, e.End); err == nil {
				resolved = &end
			}
		}
		out = append(out, adapters.Incident{
			SourceID:   fmt.Sprintf("%d", e.ID),
			Name:       e.Summary,
			Status:     e.State,
			Impact:     e.Severity,
			StartedAt:  started.UTC(),
			ResolvedAt: resolved,
		})
	}
	return out
}

func (p *Paypal) get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("paypal: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("paypal: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("paypal: read %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, fmt.Errorf("paypal: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func init() {
	adapters.Register(NewPaypal(nil))
}
