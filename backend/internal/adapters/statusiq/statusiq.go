// Package statusiq reads status pages built with StatusIQ, the status page
// product from Site24x7. The page carries an encoded page id in its inline
// config, and the components, incidents and maintenance come from a JSON API
// under /sp/api, so both are read instead of the rendered page.
package statusiq

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

const KindStatusIQ = "statusiq"

const (
	defaultAPIBase = "/sp/api/"
	summaryPath    = "public/summary_details/statuspages/"
)

// pageIDPattern finds the id the page hands its own app. The value is base64,
// so it is taken as written rather than decoded.
var pageIDPattern = regexp.MustCompile(`"enc_statuspage_id":"([^"]+)"`)

// StatusIQ names six states with one number each. Informational is a note, not
// a fault, so it reads as up, the same way xAI reads its own info state.
var statusiqHealth = map[int]adapters.Health{
	1: adapters.HealthOK,
	2: adapters.HealthOK,
	3: adapters.HealthDegraded,
	4: adapters.HealthMaintenance,
	5: adapters.HealthPartial,
	6: adapters.HealthMajor,
}

// The same numbers carry an incident's severity, written as the impact the site
// shows beside it.
var statusiqImpact = map[int]string{
	2: "informational",
	3: "degraded",
	4: "maintenance",
	5: "partial",
	6: "major",
}

type StatusIQ struct {
	client *http.Client
}

func NewStatusIQ(client *http.Client) *StatusIQ {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &StatusIQ{client: client}
}

func (s *StatusIQ) Kind() string { return KindStatusIQ }

type statusiqSummary struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Active      []statusiqIncident    `json:"active_incident_details"`
		Components  []statusiqComponent   `json:"current_status"`
		Maintenance []statusiqMaintenance `json:"maintenance_details"`
		Resolved    []statusiqResolvedDay `json:"resolved_incident_details"`
	} `json:"data"`
}

type statusiqComponent struct {
	ID          string              `json:"enc_component_id"`
	Name        string              `json:"display_name"`
	Status      int                 `json:"component_status"`
	IsGroup     bool                `json:"is_group"`
	GroupID     string              `json:"enc_componentgroup_id"`
	GroupName   string              `json:"componentgroup_display_name"`
	GroupStatus int                 `json:"componentgroup_status"`
	Children    []statusiqComponent `json:"componentgroup_components"`
}

type statusiqIncident struct {
	ID         string           `json:"enc_inc_id"`
	IncidentID string           `json:"enc_incident_id"`
	Title      string           `json:"incident_title"`
	BeganAt    string           `json:"incident_began_at"`
	EndedAt    string           `json:"incident_ended_at"`
	SeverityID int              `json:"incident_severity_id"`
	Updates    []statusiqUpdate `json:"incident_status_updates"`
}

type statusiqUpdate struct {
	Status    string `json:"incident_status"`
	UpdatedAt string `json:"status_updated_at"`
}

type statusiqResolvedDay struct {
	Incidents []statusiqIncident `json:"grp_by_incidents_list"`
}

type statusiqMaintenance struct {
	ID      string `json:"enc_maintenance_id"`
	Title   string `json:"maintenance_title"`
	Status  string `json:"maintenance_status"`
	BeginAt string `json:"maintenance_will_begin_at"`
	EndAt   string `json:"maintenance_will_end_at"`
}

func (s *StatusIQ) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	pageBody, status, retryAfter, err := s.get(ctx, t.URL, "text/html")
	if err != nil {
		return adapters.Report{HTTPStatus: status, RetryAfter: retryAfter, Raw: pageBody}, err
	}

	match := pageIDPattern.FindSubmatch(pageBody)
	if match == nil {
		return adapters.Report{HTTPStatus: status, Raw: pageBody},
			fmt.Errorf("statusiq: %s is not a StatusIQ page", t.URL)
	}
	pageID := string(match[1])

	base, err := url.Parse(t.URL)
	if err != nil {
		return adapters.Report{HTTPStatus: status, Raw: pageBody},
			fmt.Errorf("statusiq: read %s: %w", t.URL, err)
	}
	root := strings.TrimRight(t.Endpoint, "/")
	if root == "" {
		root = strings.TrimRight(defaultAPIBase, "/")
	}
	apiURL := fmt.Sprintf("%s://%s%s/%s%s?period=2&timezone=Etc/UTC",
		base.Scheme, base.Host, root, summaryPath, pageID)

	body, status, retryAfter, err := s.get(ctx, apiURL, "application/json")
	if err != nil {
		return adapters.Report{HTTPStatus: status, RetryAfter: retryAfter, Raw: body}, err
	}

	var summary statusiqSummary
	if err := json.Unmarshal(body, &summary); err != nil {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("statusiq: decode %s: %w", apiURL, err)
	}
	if summary.Code != 0 {
		message := strings.TrimSpace(summary.Message)
		if message == "" {
			message = "no message"
		}
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("statusiq: %s answered code %d: %s", apiURL, summary.Code, message)
	}

	rep := adapters.Report{
		PageID:     pageID,
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Raw:        body,
	}

	include := adapters.LowerSet(t.Include)
	position := 0
	var statuses []adapters.Health

	add := func(id, name string, health adapters.Health, group string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		position++
		statuses = append(statuses, health)
		if len(include) > 0 && !include[strings.ToLower(name)] {
			return
		}
		if id == "" {
			id = name
		}
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: id,
			Name:     name,
			Group:    group,
			Position: position,
			Status:   health,
		})
	}

	// A group is the heading of a row of components, so the children are the
	// components. A group with nothing under it is still shown, since dropping
	// it would hide a real row.
	for _, component := range summary.Data.Components {
		if component.IsGroup {
			group := strings.TrimSpace(component.GroupName)
			if len(component.Children) > 0 {
				for _, child := range component.Children {
					add(child.ID, child.Name, statusiqHealthOf(child.Status), group)
				}
				continue
			}
			add(component.GroupID, component.GroupName, statusiqHealthOf(component.GroupStatus), "")
			continue
		}
		add(component.ID, component.Name, statusiqHealthOf(component.Status), "")
	}
	if position == 0 {
		return adapters.Report{HTTPStatus: status, Raw: body},
			fmt.Errorf("statusiq: %s lists no components", t.URL)
	}

	// An ongoing incident can be worse than every component it affects, so the
	// page's own state is the worst of both. Upcoming maintenance is a plan, not
	// a current state, and is left out of this.
	for _, incident := range summary.Data.Active {
		if health := statusiqImpactHealth(incident.SeverityID); health != adapters.HealthUnknown {
			statuses = append(statuses, health)
		}
	}
	rep.Global = adapters.Worst(statuses...)

	seen := make(map[string]bool)
	addIncident := func(item adapters.Incident) {
		if item.SourceID == "" || seen[item.SourceID] {
			return
		}
		seen[item.SourceID] = true
		rep.Incidents = append(rep.Incidents, item)
	}

	for _, incident := range summary.Data.Active {
		addIncident(statusiqIncidentOf(incident, false))
	}
	for _, day := range summary.Data.Resolved {
		for _, incident := range day.Incidents {
			addIncident(statusiqIncidentOf(incident, true))
		}
	}
	for _, maintenance := range summary.Data.Maintenance {
		if status := strings.TrimSpace(maintenance.Status); status != "" && status != "V" {
			continue
		}
		addIncident(adapters.Incident{
			SourceID:  strings.TrimSpace(maintenance.ID),
			Name:      strings.TrimSpace(maintenance.Title),
			Status:    "scheduled",
			Impact:    "maintenance",
			StartedAt: statusiqTime(maintenance.BeginAt),
		})
	}

	return rep, nil
}

func statusiqIncidentOf(incident statusiqIncident, resolved bool) adapters.Incident {
	id := strings.TrimSpace(incident.ID)
	if id == "" {
		id = strings.TrimSpace(incident.IncidentID)
	}

	item := adapters.Incident{
		SourceID:  id,
		Name:      strings.TrimSpace(incident.Title),
		Status:    statusiqStatusOf(incident, resolved),
		Impact:    statusiqImpact[incident.SeverityID],
		StartedAt: statusiqTime(incident.BeganAt),
	}
	if ended := statusiqTime(incident.EndedAt); !ended.IsZero() {
		item.ResolvedAt = &ended
	}
	return item
}

// statusiqStatusOf reads the newest update the page posted. A finished incident
// whose last word is Completed reads as resolved, and an incident with no
// update yet falls back to the state its severity implies.
func statusiqStatusOf(incident statusiqIncident, resolved bool) string {
	if len(incident.Updates) > 0 {
		word := strings.ToLower(strings.TrimSpace(incident.Updates[0].Status))
		switch word {
		case "completed", "resolved":
			return "resolved"
		case "in progress":
			return "in_progress"
		}
		if word != "" {
			return strings.ReplaceAll(word, " ", "_")
		}
	}
	if resolved {
		return "resolved"
	}
	if incident.SeverityID == 4 {
		return "scheduled"
	}
	return "investigating"
}

// statusiqTime reads the timestamps the API writes, which carry an offset
// without a colon rather than the colon RFC 3339 expects.
func statusiqTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05-0700",
		time.RFC3339,
		"2006-01-02T15:04:05",
	} {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC()
		}
	}
	return time.Time{}
}

func statusiqHealthOf(state int) adapters.Health {
	if health, ok := statusiqHealth[state]; ok {
		return health
	}
	return adapters.HealthUnknown
}

func statusiqImpactHealth(severity int) adapters.Health {
	switch severity {
	case 6:
		return adapters.HealthMajor
	case 5:
		return adapters.HealthPartial
	case 4:
		return adapters.HealthMaintenance
	case 3:
		return adapters.HealthDegraded
	case 2:
		return adapters.HealthOK
	default:
		return adapters.HealthUnknown
	}
}

func (s *StatusIQ) get(ctx context.Context, target, accept string) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("statusiq: build request: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("statusiq: request %s: %w", target, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, resp.StatusCode, 0, fmt.Errorf("statusiq: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, adapters.RetryAfter(resp),
			fmt.Errorf("statusiq: %s returned HTTP %d", target, resp.StatusCode)
	}
	return body, resp.StatusCode, 0, nil
}

func init() {
	adapters.Register(NewStatusIQ(nil))
}
