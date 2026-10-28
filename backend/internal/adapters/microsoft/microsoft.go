// Package microsoft reads Microsoft 365's consumer status page, whose
// workloads are served as plain json.
package microsoft

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

const KindMicrosoft = "microsoft"

const defaultEndpoint = "/api/posts/m365Consumer"

var microsoftHealth = map[string]adapters.Health{
	"available":             adapters.HealthOK,
	"operational":           adapters.HealthOK,
	"service restored":      adapters.HealthOK,
	"degraded":              adapters.HealthDegraded,
	"degraded performance":  adapters.HealthDegraded,
	"service degradation":   adapters.HealthDegraded,
	"maintenance":           adapters.HealthMaintenance,
	"planned maintenance":   adapters.HealthMaintenance,
	"scheduled maintenance": adapters.HealthMaintenance,
	"under maintenance":     adapters.HealthMaintenance,
	"unknown":               adapters.HealthUnknown,
}

type Microsoft struct {
	client *http.Client
}

func NewMicrosoft(client *http.Client) *Microsoft {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Microsoft{client: client}
}

func (m *Microsoft) Kind() string { return KindMicrosoft }

type workload struct {
	ServiceDisplayName  string `json:"ServiceDisplayName"`
	ServiceWorkloadName string `json:"ServiceWorkloadName"`
	LastUpdatedTime     string `json:"LastUpdatedTime"`
	Status              string `json:"Status"`
}

func (m *Microsoft) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	endpoint, err := endpointURL(t)
	if err != nil {
		return adapters.Report{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("microsoft: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := m.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("microsoft: request %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("microsoft: read %s: %w", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("microsoft: %s returned HTTP %d", endpoint, resp.StatusCode)
	}

	var workloads []workload
	if err := json.Unmarshal(body, &workloads); err != nil {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("microsoft: decode %s: %w", endpoint, err)
	}
	if len(workloads) == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("microsoft: %s lists no workloads", endpoint)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        body,
	}

	include := adapters.LowerSet(t.Include)
	var statuses []adapters.Health
	var latest time.Time
	for i, w := range workloads {
		name := strings.TrimSpace(w.ServiceDisplayName)
		sourceID := strings.TrimSpace(w.ServiceWorkloadName)
		if name == "" {
			name = sourceID
		}
		if sourceID == "" {
			sourceID = name
		}
		if name == "" {
			continue
		}
		if len(include) > 0 && !include[strings.ToLower(name)] {
			continue
		}

		health, ok := microsoftHealth[strings.ToLower(strings.TrimSpace(w.Status))]
		if !ok {
			health = adapters.HealthUnknown
		}
		updated := parseTime(w.LastUpdatedTime)
		if updated.After(latest) {
			latest = updated
		}

		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID:  sourceID,
			Name:      name,
			Position:  i + 1,
			Status:    health,
			ChangedAt: updated,
		})
		statuses = append(statuses, health)
	}

	rep.Global = adapters.HealthUnknown
	if len(statuses) > 0 {
		rep.Global = adapters.Worst(statuses...)
	}
	rep.SourceUpdatedAt = latest

	return rep, nil
}

func endpointURL(t adapters.Target) (string, error) {
	endpoint := strings.TrimSpace(t.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}

	target, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("microsoft: parse endpoint %q: %w", endpoint, err)
	}
	if target.IsAbs() {
		return target.String(), nil
	}

	page, err := url.Parse(t.URL)
	if err != nil {
		return "", fmt.Errorf("microsoft: parse %s: %w", t.URL, err)
	}
	if page.Scheme == "" || page.Host == "" {
		return "", fmt.Errorf("microsoft: %s carries no host", t.URL)
	}

	origin := &url.URL{Scheme: page.Scheme, Host: page.Host}
	return origin.ResolveReference(target).String(), nil
}

func parseTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func init() {
	adapters.Register(NewMicrosoft(nil))
}
