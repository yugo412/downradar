package opencode

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

const KindOpencode = "opencode"

const defaultOpencodeEndpoint = "/api/status"

var opencodeStatus = map[string]adapters.Health{
	"operational": adapters.HealthOK,
	"degraded":    adapters.HealthDegraded,
	"outage":      adapters.HealthMajor,
	"unknown":     adapters.HealthUnknown,
}

type Opencode struct {
	client *http.Client
}

func NewOpencode(client *http.Client) *Opencode {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Opencode{client: client}
}

func (o *Opencode) Kind() string { return KindOpencode }

// opencodePayload is the shape of /api/status. Timestamps are milliseconds, and
// nothing carries a display name: components are keyed by a short slug and
// incidents have generated ids, so both are used exactly as they come.
type opencodePayload struct {
	UpdatedAt  int64  `json:"updatedAt"`
	Status     string `json:"status"`
	Stale      bool   `json:"stale"`
	Components []struct {
		Service string `json:"service"`
		Status  string `json:"status"`
	} `json:"components"`
	Incidents []struct {
		ID         string `json:"id"`
		Event      string `json:"event"`
		Service    string `json:"service"`
		Status     string `json:"status"`
		Reason     string `json:"reason"`
		StartedAt  int64  `json:"startedAt"`
		ResolvedAt int64  `json:"resolvedAt"`
	} `json:"incidents"`
	Notes []struct {
		Event string `json:"event"`
		Body  string `json:"body"`
	} `json:"notes"`
}

func (o *Opencode) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultOpencodeEndpoint
	}
	url := strings.TrimRight(t.URL, "/") + "/" + strings.TrimLeft(endpoint, "/")

	body, status, retryAfter, err := o.get(ctx, url)
	if err != nil {
		return adapters.Report{HTTPStatus: status, RetryAfter: retryAfter, Raw: body}, err
	}

	var payload opencodePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("opencode: decode json: %w", err)
	}
	if payload.Status == "" {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("opencode: %s returned no status", url)
	}

	rep := adapters.Report{
		FetchedAt:       time.Now().UTC(),
		SourceUpdatedAt: opencodeTime(payload.UpdatedAt),
		HTTPStatus:      status,
		Raw:             body,
		Global:          opencodeHealth(payload.Status, payload.Stale),
	}

	include := adapters.LowerSet(t.Include)
	for i, c := range payload.Components {
		if c.Service == "" {
			continue
		}
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(c.Service))] {
			continue
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: c.Service,
			Name:     c.Service,
			Position: i + 1,
			Status:   opencodeHealth(c.Status, payload.Stale),
		})
	}

	notes := make(map[string]string, len(payload.Notes))
	for _, n := range payload.Notes {
		if n.Event != "" && n.Body != "" {
			notes[n.Event] = n.Body
		}
	}

	for _, inc := range payload.Incidents {
		item := adapters.Incident{
			SourceID:  inc.ID,
			Name:      opencodeIncidentName(inc.Service, inc.Reason, notes[inc.Event]),
			Status:    inc.Status,
			Impact:    inc.Reason,
			StartedAt: opencodeTime(inc.StartedAt),
		}
		if inc.ResolvedAt > 0 {
			resolved := opencodeTime(inc.ResolvedAt)
			item.ResolvedAt = &resolved
		}
		rep.Incidents = append(rep.Incidents, item)
	}

	return rep, nil
}

// opencodeHealth maps the page's vocabulary. stale means the page itself has
// stopped updating, so an "operational" reading cannot be trusted.
func opencodeHealth(status string, stale bool) adapters.Health {
	if stale {
		return adapters.HealthUnknown
	}
	if health, ok := opencodeStatus[strings.ToLower(strings.TrimSpace(status))]; ok {
		return health
	}
	return adapters.HealthUnknown
}

// opencodeIncidentName builds a title, because the page has none: id and event
// look like "console-recorded-1792018800000". The written update lives in notes,
// so its first sentence is used when there is one.
func opencodeIncidentName(service, reason, note string) string {
	if note != "" {
		if sentence, _, found := strings.Cut(note, ". "); found {
			return sentence
		}
		return note
	}
	if reason != "" && reason != "none" {
		return service + " " + reason
	}
	return service
}

func opencodeTime(millis int64) time.Time {
	if millis <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(millis).UTC()
}

func (o *Opencode) get(ctx context.Context, url string) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("opencode: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("opencode: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, 0, fmt.Errorf("opencode: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, adapters.RetryAfter(resp), fmt.Errorf("opencode: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, 0, nil
}

func init() {
	adapters.Register(NewOpencode(nil))
}
