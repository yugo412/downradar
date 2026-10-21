// Package statuscake reads status pages hosted by StatusCake. The page is
// rendered on the server, so its status bar and its parent services are read
// from the html.
package statuscake

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindStatusCake = "statuscake"

var (
	barPattern    = regexp.MustCompile(`<div class="notification is-([a-z]+)"[^>]*id="status-bar"`)
	parentPattern = regexp.MustCompile(
		`(?s)<div class="singleton-status singleton-parent[^"]*">(.*?)<div class="singleton-children">`)
	parentNamePattern  = regexp.MustCompile(`<div class="level-left">\s*<span>([^<]+)</span>`)
	parentLabelPattern = regexp.MustCompile(`<span class="tag is-[a-z]+">([^<]+)</span>`)
)

var statuscakeBar = map[string]adapters.Health{
	"success": adapters.HealthOK,
	"info":    adapters.HealthOK,
	"primary": adapters.HealthOK,
	"warning": adapters.HealthDegraded,
	"danger":  adapters.HealthMajor,
}

var statuscakeLabel = map[string]adapters.Health{
	"good service":         adapters.HealthOK,
	"degraded performance": adapters.HealthDegraded,
	"partial outage":       adapters.HealthPartial,
	"service outage":       adapters.HealthMajor,
	"major outage":         adapters.HealthMajor,
	"under maintenance":    adapters.HealthMaintenance,
	"maintenance":          adapters.HealthMaintenance,
}

type StatusCake struct {
	client *http.Client
}

func NewStatusCake(client *http.Client) *StatusCake {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &StatusCake{client: client}
}

func (s *StatusCake) Kind() string { return KindStatusCake }

func (s *StatusCake) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("statuscake: build request: %w", err)
	}
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("statuscake: request %s: %w", t.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return adapters.Report{}, fmt.Errorf("statuscake: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("statuscake: %s returned HTTP %d", t.URL, resp.StatusCode)
	}

	bar := barPattern.FindSubmatch(body)
	parents := parentPattern.FindAllSubmatch(body, -1)
	if bar == nil || len(parents) == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("statuscake: %s is not a StatusCake page", t.URL)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        body,
	}

	include := adapters.LowerSet(t.Include)
	var statuses []adapters.Health
	for _, parent := range parents {
		name := parentNamePattern.FindSubmatch(parent[1])
		label := parentLabelPattern.FindSubmatch(parent[1])
		if name == nil || label == nil {
			continue
		}
		service := strings.TrimSpace(string(name[1]))
		if len(include) > 0 && !include[strings.ToLower(service)] {
			continue
		}
		health, ok := statuscakeLabel[strings.ToLower(strings.TrimSpace(string(label[1])))]
		if !ok {
			health = adapters.HealthUnknown
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: service,
			Name:     service,
			Position: len(rep.Components) + 1,
			Status:   health,
		})
		statuses = append(statuses, health)
	}
	if len(rep.Components) == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("statuscake: %s lists no services", t.URL)
	}

	global, ok := statuscakeBar[string(bar[1])]
	if !ok {
		global = adapters.HealthUnknown
	}
	if worst := adapters.Worst(statuses...); worst.Severity() > global.Severity() {
		global = worst
	}
	rep.Global = global
	return rep, nil
}

func init() {
	adapters.Register(NewStatusCake(nil))
}
