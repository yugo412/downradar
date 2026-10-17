package statusio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindStatusio = "statusio"

const defaultStatusioEndpoint = "/1.0/status/"

// The page carries its own id, under a few spellings of the same name:
// "var statuspageId = '...'", "page_id: \"...\"" and so on. The id is what the
// public API is addressed by, so it is read from the page rather than configured.
var statusioPageID = regexp.MustCompile(`(?i)(?:statuspage_?id|page_?id)\s*[:=]\s*["']([0-9a-f]{24})["']`)

// Status.io numbers its states and documents the labels: 100 Operational,
// 200 Planned maintenance, 300 Degraded performance, 400 Partial service
// disruption, 500 Service disruption, 600 Security issue. A security issue is
// reported as major, because it is an active problem rather than a quiet one.
var statusioCode = map[int]adapters.Health{
	100: adapters.HealthOK,
	200: adapters.HealthMaintenance,
	300: adapters.HealthDegraded,
	400: adapters.HealthPartial,
	500: adapters.HealthMajor,
	600: adapters.HealthMajor,
}

type Statusio struct {
	client *http.Client
}

func NewStatusio(client *http.Client) *Statusio {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Statusio{client: client}
}

func (s *Statusio) Kind() string { return KindStatusio }

type statusioPayload struct {
	Result struct {
		StatusOverall struct {
			Updated    string `json:"updated"`
			StatusCode int    `json:"status_code"`
		} `json:"status_overall"`
		Status []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			StatusCode int    `json:"status_code"`
			Containers []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				StatusCode int    `json:"status_code"`
			} `json:"containers"`
		} `json:"status"`
	} `json:"result"`
}

func (s *Statusio) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	base := strings.TrimRight(t.URL, "/")

	page, status, err := s.get(ctx, base+"/", "text/html")
	if err != nil {
		return adapters.Report{HTTPStatus: status, Raw: page}, err
	}

	match := statusioPageID.FindSubmatch(page)
	if match == nil {
		return adapters.Report{HTTPStatus: status, Raw: page},
			fmt.Errorf("statusio: no page id found on %s", t.URL)
	}
	pageID := string(match[1])

	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultStatusioEndpoint
	}
	url := base + "/" + strings.TrimLeft(endpoint, "/") + pageID

	body, status, err := s.get(ctx, url, "application/json")
	if err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body}, err
	}

	var payload statusioPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("statusio: decode json: %w", err)
	}
	if payload.Result.StatusOverall.StatusCode == 0 {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("statusio: %s returned no status_overall", url)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        body,
		PageID:     pageID,
		Global:     statusioHealth(payload.Result.StatusOverall.StatusCode),
	}
	if updated, err := time.Parse(time.RFC3339, payload.Result.StatusOverall.Updated); err == nil {
		rep.SourceUpdatedAt = updated.UTC()
	}

	// The top level of "status" is a group with a rolled up status of its own.
	// Only the containers below it are reported, each carrying its group name.
	include := adapters.LowerSet(t.Include)
	position := 0
	for _, group := range payload.Result.Status {
		for _, container := range group.Containers {
			if container.ID == "" {
				continue
			}
			if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(container.Name))] {
				continue
			}
			position++
			rep.Components = append(rep.Components, adapters.ComponentStatus{
				SourceID: container.ID,
				Name:     container.Name,
				Group:    group.Name,
				Position: position,
				Status:   statusioHealth(container.StatusCode),
			})
		}
	}

	return rep, nil
}

func statusioHealth(code int) adapters.Health {
	if health, ok := statusioCode[code]; ok {
		return health
	}
	return adapters.HealthUnknown
}

func (s *Statusio) get(ctx context.Context, url, accept string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("statusio: build request: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("statusio: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("statusio: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, fmt.Errorf("statusio: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func init() {
	adapters.Register(NewStatusio(nil))
}
