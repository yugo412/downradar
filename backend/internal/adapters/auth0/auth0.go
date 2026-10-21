// Package auth0 reads Auth0's own status page, a Next.js app whose region
// status and incidents are serialised into the page itself.
package auth0

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindAuth0 = "auth0"

var nextDataPattern = regexp.MustCompile(
	`(?s)<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)

var auth0Impact = map[string]adapters.Health{
	"none":        adapters.HealthOK,
	"maintenance": adapters.HealthMaintenance,
	"minor":       adapters.HealthDegraded,
	"major":       adapters.HealthPartial,
	"critical":    adapters.HealthMajor,
}

type Auth0 struct {
	client *http.Client
}

func NewAuth0(client *http.Client) *Auth0 {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Auth0{client: client}
}

func (a *Auth0) Kind() string { return KindAuth0 }

type auth0Page struct {
	Props struct {
		PageProps struct {
			ActiveIncidents []struct {
				Region   string `json:"region"`
				Response struct {
					Incidents []auth0Incident `json:"incidents"`
				} `json:"response"`
			} `json:"activeIncidents"`
			Timeline map[string]map[string]json.RawMessage `json:"timeline"`
		} `json:"pageProps"`
	} `json:"props"`
}

type auth0Incident struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Impact     string     `json:"impact"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

func (a *Auth0) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("auth0: build request: %w", err)
	}
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("auth0: request %s: %w", t.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return adapters.Report{}, fmt.Errorf("auth0: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("auth0: %s returned HTTP %d", t.URL, resp.StatusCode)
	}

	match := nextDataPattern.FindSubmatch(body)
	if match == nil {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("auth0: %s carries no page data", t.URL)
	}

	var page auth0Page
	if err := json.Unmarshal(match[1], &page); err != nil {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("auth0: decode page data: %w", err)
	}
	props := page.Props.PageProps
	if len(props.Timeline) == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("auth0: %s lists no regions", t.URL)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        body,
	}

	// A region is what the page lets a reader choose between, so it is what is
	// offered here as a component.
	regions := make([]string, 0, len(props.Timeline))
	for region := range props.Timeline {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	regionStatus := map[string]adapters.Health{}
	seen := map[string]bool{}
	for _, item := range props.ActiveIncidents {
		region := adapters.HealthOK
		for _, inc := range item.Response.Incidents {
			// A scheduled maintenance and a resolved incident are both written
			// down here without the region being out of order now.
			if inc.Status != "scheduled" && inc.Status != "resolved" && inc.Status != "completed" {
				health, ok := auth0Impact[inc.Impact]
				if !ok {
					health = adapters.HealthUnknown
				}
				if health.Severity() > region.Severity() {
					region = health
				}
			}

			if inc.ID == "" || seen[inc.ID] {
				continue
			}
			seen[inc.ID] = true
			rep.Incidents = append(rep.Incidents, adapters.Incident{
				SourceID:   inc.ID,
				Name:       inc.Name,
				Status:     inc.Status,
				Impact:     inc.Impact,
				StartedAt:  inc.StartedAt.UTC(),
				ResolvedAt: inc.ResolvedAt,
			})
		}
		if len(item.Response.Incidents) == 0 {
			continue
		}
		if current, ok := regionStatus[item.Region]; !ok || region.Severity() > current.Severity() {
			regionStatus[item.Region] = region
		}
	}

	include := adapters.LowerSet(t.Include)
	var statuses []adapters.Health
	for i, region := range regions {
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(region))] {
			continue
		}
		health, ok := regionStatus[region]
		if !ok {
			health = adapters.HealthOK
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: region,
			Name:     region,
			Position: i + 1,
			Status:   health,
		})
		statuses = append(statuses, health)
	}

	rep.Global = adapters.HealthUnknown
	if len(statuses) > 0 {
		rep.Global = adapters.Worst(statuses...)
	}
	return rep, nil
}

func init() {
	adapters.Register(NewAuth0(nil))
}
