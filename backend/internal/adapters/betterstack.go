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

const KindBetterstack = "betterstack"

const defaultBetterstackEndpoint = "/index.json"

// Better Stack names its states its own way: a resource in trouble is in
// "downtime" rather than "major_outage", and a resource that is listed but not
// watched reports "not_monitored".
var betterstackState = map[string]Health{
	"operational":   HealthOK,
	"degraded":      HealthDegraded,
	"downtime":      HealthMajor,
	"maintenance":   HealthMaintenance,
	"not_monitored": HealthUnknown,
}

type Betterstack struct {
	client *http.Client
}

func NewBetterstack(client *http.Client) *Betterstack {
	if client == nil {
		client = DefaultClient()
	}
	return &Betterstack{client: client}
}

func (b *Betterstack) Kind() string { return KindBetterstack }

// A Better Stack page publishes a JSON:API document at /index.json. The page is
// the data member and everything on it — the sections, the resources and the
// incident reports — travels in included, told apart by type.
type bsPage struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Subdomain      string    `json:"subdomain"`
			AggregateState string    `json:"aggregate_state"`
			UpdatedAt      time.Time `json:"updated_at"`
		} `json:"attributes"`
	} `json:"data"`
	Included []bsIncluded `json:"included"`
}

// bsIncluded carries the attributes of every kind of thing the document can
// include. A field that does not belong to the type in hand stays empty.
type bsIncluded struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		// A section.
		Name string `json:"name"`
		// A resource.
		Status              string `json:"status"`
		PublicName          string `json:"public_name"`
		StatusPageSectionID int64  `json:"status_page_section_id"`
		Position            int    `json:"position"`
		// An incident report.
		Title          string     `json:"title"`
		ReportType     string     `json:"report_type"`
		StartsAt       *time.Time `json:"starts_at"`
		EndsAt         *time.Time `json:"ends_at"`
		AggregateState string     `json:"aggregate_state"`
	} `json:"attributes"`
}

func (b *Betterstack) Fetch(ctx context.Context, t Target) (Report, error) {
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultBetterstackEndpoint
	}
	url := strings.TrimRight(t.URL, "/") + "/" + strings.TrimLeft(endpoint, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Report{}, fmt.Errorf("betterstack: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := b.client.Do(req)
	if err != nil {
		return Report{}, fmt.Errorf("betterstack: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Report{}, fmt.Errorf("betterstack: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("betterstack: %s returned HTTP %d", url, resp.StatusCode)
	}

	var page bsPage
	if err := json.Unmarshal(body, &page); err != nil {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("betterstack: decode json: %w", err)
	}
	// The document has to say it is a status page, or any page that happens to
	// serve an /index.json would be read as one.
	if page.Data.Type != "status_page" || page.Data.ID == "" {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("betterstack: %s is not a Better Stack status page", url)
	}

	rep := Report{
		FetchedAt:       time.Now().UTC(),
		SourceUpdatedAt: page.Data.Attributes.UpdatedAt.UTC(),
		PageID:          page.Data.Attributes.Subdomain,
		HTTPStatus:      resp.StatusCode,
		Raw:             body,
	}
	if rep.PageID == "" {
		rep.PageID = page.Data.ID
	}

	sections := make(map[string]string)
	for _, item := range page.Included {
		if item.Type == "status_page_section" {
			sections[item.ID] = item.Attributes.Name
		}
	}

	include := lowerSet(t.Include)

	var statuses []Health
	for _, item := range page.Included {
		if item.Type != "status_page_resource" {
			continue
		}
		// A resource that is listed but not watched has no status to report, so
		// it is left out rather than shown as an unknown one.
		if item.Attributes.Status == "not_monitored" {
			continue
		}

		name := item.Attributes.PublicName
		if name == "" {
			name = item.ID
		}
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}

		health, ok := betterstackState[item.Attributes.Status]
		if !ok {
			health = HealthUnknown
		}
		rep.Components = append(rep.Components, ComponentStatus{
			SourceID: item.ID,
			Name:     name,
			Group:    sections[strconv.FormatInt(item.Attributes.StatusPageSectionID, 10)],
			Position: item.Attributes.Position,
			Status:   health,
		})
		statuses = append(statuses, health)
	}

	// The page reports its own overall state, and it is the one to trust: it is
	// what the page itself shows at the top. Only when it names a state the
	// adapter does not know does the worst resource stand in for it.
	global, ok := betterstackState[page.Data.Attributes.AggregateState]
	if !ok || global == HealthUnknown {
		global = Worst(statuses...)
	}
	rep.Global = global

	for _, item := range page.Included {
		if item.Type != "status_report" || item.Attributes.StartsAt == nil {
			continue
		}
		var resolvedAt *time.Time
		if item.Attributes.EndsAt != nil {
			ended := item.Attributes.EndsAt.UTC()
			resolvedAt = &ended
		}
		rep.Incidents = append(rep.Incidents, Incident{
			SourceID:   item.ID,
			Name:       item.Attributes.Title,
			Status:     item.Attributes.AggregateState,
			Impact:     item.Attributes.ReportType,
			StartedAt:  item.Attributes.StartsAt.UTC(),
			ResolvedAt: resolvedAt,
		})
	}

	return rep, nil
}

func init() {
	Register(NewBetterstack(nil))
}
