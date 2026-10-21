// Package midtrans reads Midtrans' status page. There is no feed behind it: the
// overall line and one row per service are rendered into the page.
package midtrans

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

const KindMidtrans = "midtrans"

var (
	headPattern  = regexp.MustCompile(`(?s)<div class="status-head[^"]*">\s*<h2>([^<]+)</h2>`)
	namePattern  = regexp.MustCompile(`(?s)<h3>([^<]+)</h3>`)
	badgePattern = regexp.MustCompile(`(?s)<span class="status-badge">([^<]+)</span>`)
	statePattern = regexp.MustCompile(`(?s)<div class="status-opt">\s*([^<]+?)\s*</div>`)
)

var midtransState = map[string]adapters.Health{
	"operational":             adapters.HealthOK,
	"all systems operational": adapters.HealthOK,
	"normal":                  adapters.HealthOK,
	"maintenance":             adapters.HealthMaintenance,
	"under maintenance":       adapters.HealthMaintenance,
	"degraded":                adapters.HealthDegraded,
	"service disruption":      adapters.HealthDegraded,
	"partial outage":          adapters.HealthPartial,
	"service outage":          adapters.HealthMajor,
	"outage":                  adapters.HealthMajor,
}

type Midtrans struct {
	client *http.Client
}

func NewMidtrans(client *http.Client) *Midtrans {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Midtrans{client: client}
}

func (m *Midtrans) Kind() string { return KindMidtrans }

func (m *Midtrans) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("midtrans: build request: %w", err)
	}
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := m.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("midtrans: request %s: %w", t.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return adapters.Report{}, fmt.Errorf("midtrans: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("midtrans: %s returned HTTP %d", t.URL, resp.StatusCode)
	}

	head := headPattern.FindSubmatch(body)
	// Each service is a block that starts with its own row, so splitting on the
	// row tag keeps the small patterns below from running across services.
	blocks := strings.Split(string(body), `<div class="status-list">`)
	if head == nil || len(blocks) < 2 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("midtrans: %s is not the status page", t.URL)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        body,
	}

	include := adapters.LowerSet(t.Include)
	var statuses []adapters.Health
	for _, block := range blocks[1:] {
		name := namePattern.FindStringSubmatch(block)
		state := statePattern.FindStringSubmatch(block)
		if name == nil || state == nil {
			continue
		}
		service := strings.TrimSpace(name[1])
		if len(include) > 0 && !include[strings.ToLower(service)] {
			continue
		}

		health, ok := midtransState[strings.ToLower(strings.TrimSpace(state[1]))]
		if !ok {
			health = adapters.HealthUnknown
		}
		group := ""
		if badge := badgePattern.FindStringSubmatch(block); badge != nil {
			group = strings.TrimSpace(badge[1])
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: service,
			Name:     service,
			Group:    group,
			Position: len(rep.Components) + 1,
			Status:   health,
		})
		statuses = append(statuses, health)
	}
	if len(rep.Components) == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("midtrans: %s lists no services", t.URL)
	}

	global, ok := midtransState[strings.ToLower(strings.TrimSpace(string(head[1])))]
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
	adapters.Register(NewMidtrans(nil))
}
