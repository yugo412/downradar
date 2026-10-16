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

const KindSorryapp = "sorryapp"

const defaultSorryappEndpoint = "/api/v1/"

// Sorry's own table of notice statuses maps onto just two component states:
// Investigating and Identified show as degraded, and everything else, including
// planned maintenance, shows as operational.
var sorryState = map[string]Health{
	"operational": HealthOK,
	"degraded":    HealthDegraded,
}

type Sorryapp struct {
	client *http.Client
}

func NewSorryapp(client *http.Client) *Sorryapp {
	if client == nil {
		client = DefaultClient()
	}
	return &Sorryapp{client: client}
}

func (s *Sorryapp) Kind() string { return KindSorryapp }

type sorryPage struct {
	Page struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		State     string `json:"state"`
		UpdatedAt string `json:"updated_at"`
	} `json:"page"`
}

type sorryComponent struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	ParentID *int64 `json:"parent_id"`
	Position int    `json:"position"`
}

type sorryNotice struct {
	ID      int64   `json:"id"`
	Type    string  `json:"type"`
	State   string  `json:"state"`
	Subject string  `json:"subject"`
	URL     string  `json:"url"`
	BeganAt string  `json:"began_at"`
	EndedAt *string `json:"ended_at"`
}

func (s *Sorryapp) Fetch(ctx context.Context, t Target) (Report, error) {
	base := strings.TrimRight(t.URL, "/")
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultSorryappEndpoint
	}
	root := base + "/" + strings.TrimLeft(endpoint, "/")

	statusBody, status, err := s.get(ctx, root+"status")
	if err != nil {
		return Report{HTTPStatus: status, Raw: statusBody}, err
	}

	var page sorryPage
	if err := json.Unmarshal(statusBody, &page); err != nil {
		return Report{HTTPStatus: status, Raw: statusBody},
			fmt.Errorf("sorryapp: decode status: %w", err)
	}
	if page.Page.State == "" {
		return Report{HTTPStatus: status, Raw: statusBody},
			fmt.Errorf("sorryapp: %s returned no page state", root+"status")
	}

	rep := Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        statusBody,
		Global:     sorryHealth(page.Page.State),
	}
	if page.Page.ID > 0 {
		rep.PageID = strconv.FormatInt(page.Page.ID, 10)
	}
	if updated, err := time.Parse(time.RFC3339, page.Page.UpdatedAt); err == nil {
		rep.SourceUpdatedAt = updated.UTC()
	}

	componentsBody, _, err := s.get(ctx, root+"components")
	if err != nil {
		return rep, err
	}
	var components struct {
		Components []sorryComponent `json:"components"`
	}
	if err := json.Unmarshal(componentsBody, &components); err != nil {
		return rep, fmt.Errorf("sorryapp: decode components: %w", err)
	}

	// Two levels at most. A component with children is reported as well, and the
	// children carry its name as their group.
	names := make(map[int64]string, len(components.Components))
	for _, c := range components.Components {
		names[c.ID] = c.Name
	}

	include := lowerSet(t.Include)
	for _, c := range components.Components {
		if c.Name == "" {
			continue
		}
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(c.Name))] {
			continue
		}

		group := ""
		if c.ParentID != nil {
			group = names[*c.ParentID]
		}

		rep.Components = append(rep.Components, ComponentStatus{
			SourceID: strconv.FormatInt(c.ID, 10),
			Name:     c.Name,
			Group:    group,
			Position: c.Position,
			Status:   sorryHealth(c.State),
		})
	}

	noticesBody, _, err := s.get(ctx, root+"notices")
	if err != nil {
		return rep, err
	}
	var notices struct {
		Notices []sorryNotice `json:"notices"`
	}
	if err := json.Unmarshal(noticesBody, &notices); err != nil {
		return rep, fmt.Errorf("sorryapp: decode notices: %w", err)
	}

	for _, n := range notices.Notices {
		item := Incident{
			SourceID: strconv.FormatInt(n.ID, 10),
			Name:     n.Subject,
			Status:   n.State,
			Impact:   n.Type,
			URL:      n.URL,
		}
		if began, err := time.Parse(time.RFC3339, n.BeganAt); err == nil {
			item.StartedAt = began.UTC()
		}
		if n.EndedAt != nil {
			if ended, err := time.Parse(time.RFC3339, *n.EndedAt); err == nil {
				resolved := ended.UTC()
				item.ResolvedAt = &resolved
			}
		}
		rep.Incidents = append(rep.Incidents, item)
	}

	return rep, nil
}

func sorryHealth(state string) Health {
	if health, ok := sorryState[strings.ToLower(strings.TrimSpace(state))]; ok {
		return health
	}
	return HealthUnknown
}

func (s *Sorryapp) get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("sorryapp: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("sorryapp: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("sorryapp: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, fmt.Errorf("sorryapp: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func init() {
	Register(NewSorryapp(nil))
}
