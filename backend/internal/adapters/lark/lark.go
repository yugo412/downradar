// Package lark reads Lark's own status page, whose web app calls a small JSON
// API for the status of each service.
package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindLark = "lark"

const defaultLarkEndpoint = "/api/service/status"

// The page has four states: Operational, Maintenance, Incident and Outage. An
// incident is a partial outage of a service; an outage is a full one.
var larkHealth = map[int]adapters.Health{
	0:  adapters.HealthOK,
	10: adapters.HealthMaintenance,
	20: adapters.HealthPartial,
	99: adapters.HealthMajor,
}

// The page names each service by its bizType. The labels are the English ones
// its own bundle carries, because the API only returns the number.
var larkServices = map[int]string{
	1:  "Messenger",
	2:  "Search",
	3:  "Calendar",
	4:  "Meetings",
	5:  "Docs",
	6:  "Email",
	7:  "Workplace",
	8:  "Admin console",
	9:  "Log in",
	10: "Open API",
	11: "Approval",
	12: "Clock in",
	13: "Lark OKR",
	14: "Lark Performance",
	15: "Lark Hire",
	16: "Lark Contract",
	17: "Lark Spend",
	18: "Contacts",
	19: "Lark CoreHR",
	20: "Meego",
	21: "Lark AI Companion",
	22: "aPaaS",
}

type Lark struct {
	client *http.Client
}

func NewLark(client *http.Client) *Lark {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Lark{client: client}
}

func (l *Lark) Kind() string { return KindLark }

type larkPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ServiceList []struct {
			BizType int  `json:"bizType"`
			Status  *int `json:"status"`
		} `json:"serviceList"`
	} `json:"data"`
}

func (l *Lark) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = defaultLarkEndpoint
	}
	url := strings.TrimRight(t.URL, "/") + "/" + strings.TrimLeft(endpoint, "/")

	body, status, retryAfter, err := l.get(ctx, url)
	if err != nil {
		return adapters.Report{HTTPStatus: status, RetryAfter: retryAfter, Raw: body}, err
	}

	var payload larkPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("lark: decode json: %w", err)
	}
	if payload.Code != 0 {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("lark: %s returned code %d: %s", url, payload.Code, payload.Message)
	}
	if len(payload.Data.ServiceList) == 0 {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("lark: %s lists no services", url)
	}

	statuses := make([]adapters.Health, 0, len(payload.Data.ServiceList))
	components := make([]adapters.ComponentStatus, 0, len(payload.Data.ServiceList))
	include := adapters.LowerSet(t.Include)

	for i, service := range payload.Data.ServiceList {
		name := larkServiceName(service.BizType)
		health := adapters.HealthUnknown
		if service.Status != nil {
			health = larkHealthOf(*service.Status)
		}
		statuses = append(statuses, health)

		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(name))] {
			continue
		}
		components = append(components, adapters.ComponentStatus{
			SourceID: strconv.Itoa(service.BizType),
			Name:     name,
			Position: i + 1,
			Status:   health,
		})
	}

	return adapters.Report{
		Global:     adapters.Worst(statuses...),
		Components: components,
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        body,
	}, nil
}

func larkServiceName(bizType int) string {
	if name, ok := larkServices[bizType]; ok {
		return name
	}
	return fmt.Sprintf("Service %d", bizType)
}

func larkHealthOf(status int) adapters.Health {
	if health, ok := larkHealth[status]; ok {
		return health
	}
	return adapters.HealthUnknown
}

func (l *Lark) get(ctx context.Context, url string) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("lark: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("lark: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, 0, fmt.Errorf("lark: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, adapters.RetryAfter(resp), fmt.Errorf("lark: %s returned HTTP %d", url, resp.StatusCode)
	}
	return body, resp.StatusCode, 0, nil
}

func init() {
	adapters.Register(NewLark(nil))
}
