package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const KindSlack = "slack"

const slackCurrentPath = "/api/v2.0.0/current"
const slackHistoryPath = "/api/v2.0.0/history"

var slackStatus = map[string]Health{
	"ok":          HealthOK,
	"notice":      HealthDegraded,
	"incident":    HealthPartial,
	"outage":      HealthMajor,
	"maintenance": HealthMaintenance,
}

type Slack struct {
	client *http.Client
}

func NewSlack(client *http.Client) *Slack {
	if client == nil {
		client = DefaultClient()
	}
	return &Slack{client: client}
}

func (s *Slack) Kind() string { return KindSlack }

type slackCurrent struct {
	Status      string `json:"status"`
	DateUpdated string `json:"date_updated"`
}

type slackIncident struct {
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Status      string   `json:"status"`
	URL         string   `json:"url"`
	DateCreated string   `json:"date_created"`
	DateUpdated string   `json:"date_updated"`
	Services    []string `json:"services"`
}

func (s *Slack) Fetch(ctx context.Context, t Target) (Report, error) {
	base := strings.TrimRight(t.URL, "/")

	currentBody, status, err := s.get(ctx, base+slackCurrentPath)
	if err != nil {
		return Report{HTTPStatus: status, Raw: currentBody}, err
	}

	var current slackCurrent
	if err := json.Unmarshal(currentBody, &current); err != nil {
		return Report{HTTPStatus: status, Raw: currentBody},
			fmt.Errorf("slack: decode current: %w", err)
	}
	if current.Status == "" {
		return Report{HTTPStatus: status, Raw: currentBody},
			fmt.Errorf("slack: %s returned no status", base+slackCurrentPath)
	}

	health, ok := slackStatus[strings.ToLower(current.Status)]
	if !ok {
		health = HealthUnknown
	}

	rep := Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        currentBody,
		Global:     health,
		Components: []ComponentStatus{{
			SourceID: "all-services",
			Name:     "All services",
			Position: 1,
			Status:   health,
		}},
	}

	historyBody, _, err := s.get(ctx, base+slackHistoryPath)
	if err != nil {
		return rep, err
	}
	var incidents []slackIncident
	if err := json.Unmarshal(historyBody, &incidents); err != nil {
		return rep, fmt.Errorf("slack: decode history: %w", err)
	}

	if updated, err := time.Parse(time.RFC3339, current.DateUpdated); err == nil {
		rep.SourceUpdatedAt = updated.UTC()
	}

	for _, inc := range incidents {
		item := Incident{
			SourceID: strconv.FormatInt(inc.ID, 10),
			Name:     inc.Title,
			Status:   inc.Status,
			URL:      inc.URL,
			Impact:   strings.Join(inc.Services, ", "),
		}
		if started, err := time.Parse(time.RFC3339, inc.DateCreated); err == nil {
			item.StartedAt = started.UTC()
		}
		if inc.Status == "resolved" {
			if updated, err := time.Parse(time.RFC3339, inc.DateUpdated); err == nil {
				resolved := updated.UTC()
				item.ResolvedAt = &resolved
			}
		}
		rep.Incidents = append(rep.Incidents, item)
	}

	return rep, nil
}

func (s *Slack) get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("slack: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("slack: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, fmt.Errorf("slack: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func init() {
	Register(NewSlack(nil))
}
