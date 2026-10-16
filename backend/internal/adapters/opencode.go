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

const KindOpencode = "opencode"

const defaultOpencodeEndpoint = "/api/status"

var opencodeStatus = map[string]Health{
	"operational": HealthOK,
	"degraded":    HealthDegraded,
	"outage":      HealthMajor,
	"unknown":     HealthUnknown,
}

type Opencode struct {
	client *http.Client
}

func NewOpencode(client *http.Client) *Opencode {
	if client == nil {
		client = DefaultClient()
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

func (o *Opencode) Fetch(ctx context.Context, t Target) (Report, error) {
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultOpencodeEndpoint
	}
	url := strings.TrimRight(t.URL, "/") + "/" + strings.TrimLeft(endpoint, "/")

	body, status, err := o.get(ctx, url)
	if err != nil {
		return Report{HTTPStatus: status, Raw: body}, err
	}

	var payload opencodePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("opencode: decode json: %w", err)
	}
	if payload.Status == "" {
		return Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("opencode: %s returned no status", url)
	}

	rep := Report{
		FetchedAt:       time.Now().UTC(),
		SourceUpdatedAt: opencodeTime(payload.UpdatedAt),
		HTTPStatus:      status,
		Raw:             body,
		Global:          opencodeHealth(payload.Status, payload.Stale),
	}

	include := lowerSet(t.Include)
	for i, c := range payload.Components {
		if c.Service == "" {
			continue
		}
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(c.Service))] {
			continue
		}
		rep.Components = append(rep.Components, ComponentStatus{
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
		item := Incident{
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
func opencodeHealth(status string, stale bool) Health {
	if stale {
		return HealthUnknown
	}
	if health, ok := opencodeStatus[strings.ToLower(strings.TrimSpace(status))]; ok {
		return health
	}
	return HealthUnknown
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

func (o *Opencode) get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("opencode: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("opencode: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("opencode: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, fmt.Errorf("opencode: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func init() {
	Register(NewOpencode(nil))
}
