// Package indodax reads Indodax's status page, a custom WordPress template that
// renders its feature table on the server.
package indodax

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindIndodax = "indodax"

var indodaxHealth = map[string]adapters.Health{
	"green":  adapters.HealthOK,
	"yellow": adapters.HealthDegraded,
	"red":    adapters.HealthMaintenance,
}

var (
	sectionPattern = regexp.MustCompile(`(?s)<section class="status-section">(.*?)</section>`)
	titlePattern   = regexp.MustCompile(`(?s)<h2 class="section-title">([^<]+)</h2>`)
	tablePattern   = regexp.MustCompile(`(?s)<table class="idx-status-table">(.*?)</table>`)
	rowPattern     = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	namePattern    = regexp.MustCompile(`(?s)<td[^>]*>\s*<strong>([^<]+)</strong>`)
	statusPattern  = regexp.MustCompile(`status-line-([a-z]+)`)
)

type Indodax struct {
	client *http.Client
}

func NewIndodax(client *http.Client) *Indodax {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Indodax{client: client}
}

func (i *Indodax) Kind() string { return KindIndodax }

func (i *Indodax) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("indodax: build request: %w", err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := i.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("indodax: request %s: %w", t.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return adapters.Report{}, fmt.Errorf("indodax: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.Report{HTTPStatus: resp.StatusCode, RetryAfter: adapters.RetryAfter(resp), Raw: body},
			fmt.Errorf("indodax: %s returned HTTP %d", t.URL, resp.StatusCode)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        body,
	}

	include := adapters.LowerSet(t.Include)
	var statuses []adapters.Health
	position := 0

	for _, section := range sectionPattern.FindAllSubmatch(body, -1) {
		group := ""
		if title := titlePattern.FindSubmatch(section[1]); title != nil {
			group = strings.TrimSpace(html.UnescapeString(string(title[1])))
		}

		table := tablePattern.FindSubmatch(section[1])
		if table == nil {
			continue
		}
		for _, row := range rowPattern.FindAllSubmatch(table[1], -1) {
			name := namePattern.FindSubmatch(row[1])
			if name == nil {
				continue
			}
			feature := strings.TrimSpace(html.UnescapeString(string(name[1])))
			if feature == "" {
				continue
			}

			health := adapters.HealthUnknown
			if status := statusPattern.FindSubmatch(row[1]); status != nil {
				if mapped, ok := indodaxHealth[string(status[1])]; ok {
					health = mapped
				}
			}

			position++
			statuses = append(statuses, health)

			if len(include) > 0 && !include[strings.ToLower(feature)] {
				continue
			}
			rep.Components = append(rep.Components, adapters.ComponentStatus{
				SourceID: feature,
				Name:     feature,
				Group:    group,
				Position: position,
				Status:   health,
			})
		}
	}

	if position == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("indodax: %s lists no features", t.URL)
	}

	// The timestamp on every row is the same fixed value, so it says nothing
	// about when a state changed and is deliberately not read.
	rep.Global = adapters.Worst(statuses...)
	return rep, nil
}

func init() {
	adapters.Register(NewIndodax(nil))
}
