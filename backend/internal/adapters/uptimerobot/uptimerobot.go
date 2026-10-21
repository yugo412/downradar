// Package uptimerobot reads status pages hosted by UptimeRobot. The page is a
// dashboard with no feed of its own, but it carries the id of the monitor list
// it draws from, and that list is public json.
package uptimerobot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindUptimeRobot = "uptimerobot"

// pageIDPattern finds the monitor list the page is built on, which is the only
// thing a profile needs: the id belongs to the page, not to the vendor. The
// link is usually absolute, so the host it names is kept.
var pageIDPattern = regexp.MustCompile(`(https?://[^"'\s]+)?(/api/getMonitorList/[A-Za-z0-9]+)`)

var uptimerobotMonitor = map[string]adapters.Health{
	"success": adapters.HealthOK,
	"warning": adapters.HealthDegraded,
	"danger":  adapters.HealthMajor,
	"paused":  adapters.HealthMaintenance,
}

type UptimeRobot struct {
	client *http.Client
}

func NewUptimeRobot(client *http.Client) *UptimeRobot {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &UptimeRobot{client: client}
}

func (u *UptimeRobot) Kind() string { return KindUptimeRobot }

type monitorList struct {
	Monitors []struct {
		ID        int64  `json:"monitorId"`
		Name      string `json:"name"`
		Status    string `json:"statusClass"`
		GroupName string `json:"groupName"`
	} `json:"data"`
}

func (u *UptimeRobot) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	page, err := u.get(ctx, t.URL)
	if err != nil {
		return adapters.Report{}, err
	}

	match := pageIDPattern.FindSubmatch(page)
	if match == nil {
		return adapters.Report{HTTPStatus: http.StatusOK, Raw: page},
			fmt.Errorf("uptimerobot: %s is not an UptimeRobot status page", t.URL)
	}
	listPath := string(match[2])
	pageID := listPath[strings.LastIndex(listPath, "/")+1:]

	base, err := url.Parse(t.URL)
	if err != nil {
		return adapters.Report{HTTPStatus: http.StatusOK, Raw: page},
			fmt.Errorf("uptimerobot: parse %s: %w", t.URL, err)
	}
	listURL := listPath
	if host := string(match[1]); host != "" {
		listURL = host + listURL
	} else {
		listURL = base.Scheme + "://" + base.Host + listURL
	}
	if t.Endpoint != "" {
		listURL = base.Scheme + "://" + base.Host + "/" + strings.TrimLeft(t.Endpoint, "/")
	}

	body, status, err := u.getWithStatus(ctx, listURL)
	if err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body}, err
	}

	var list monitorList
	if err := json.Unmarshal(body, &list); err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("uptimerobot: decode %s: %w", listURL, err)
	}
	if len(list.Monitors) == 0 {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("uptimerobot: %s lists no monitors", listURL)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		PageID:     pageID,
		HTTPStatus: status,
		Raw:        body,
	}

	include := adapters.LowerSet(t.Include)
	var statuses []adapters.Health
	for i, m := range list.Monitors {
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(m.Name))] {
			continue
		}
		health, ok := uptimerobotMonitor[m.Status]
		if !ok {
			health = adapters.HealthUnknown
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: fmt.Sprintf("%d", m.ID),
			Name:     m.Name,
			Group:    m.GroupName,
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

func (u *UptimeRobot) get(ctx context.Context, url string) ([]byte, error) {
	body, _, err := u.getWithStatus(ctx, url)
	return body, err
}

func (u *UptimeRobot) getWithStatus(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("uptimerobot: build request: %w", err)
	}
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("uptimerobot: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("uptimerobot: read %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, fmt.Errorf("uptimerobot: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func init() {
	adapters.Register(NewUptimeRobot(nil))
}
