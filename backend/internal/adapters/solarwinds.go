package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const KindSolarwinds = "solarwinds"

// The states a page names its components with, which are the slugs its own
// status codes carry. Under maintenance is the fifth one, and a component
// carries it apart from its normal status.
var solarwindsState = map[string]Health{
	"operational":       HealthOK,
	"degraded":          HealthDegraded,
	"partial-outage":    HealthPartial,
	"major-outage":      HealthMajor,
	"under-maintenance": HealthMaintenance,
}

// A SolarWinds (Squadcast) page is a Next.js app with no API of its own: the
// page, its components, its incidents and its maintenance windows all sit in
// the __NEXT_DATA__ script the page embeds.
var solarwindsNextData = regexp.MustCompile(
	`(?s)<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)

type Solarwinds struct {
	client *http.Client
}

func NewSolarwinds(client *http.Client) *Solarwinds {
	if client == nil {
		client = DefaultClient()
	}
	return &Solarwinds{client: client}
}

func (s *Solarwinds) Kind() string { return KindSolarwinds }

type swPayload struct {
	Props struct {
		PageProps swPageProps `json:"pageProps"`
	} `json:"props"`
}

type swPageProps struct {
	Details            swDetails       `json:"details"`
	Maintenance        []swMaintenance `json:"maintenance"`
	OnGoingMaintenance []swMaintenance `json:"onGoingMaintenance"`
	History            []swDay         `json:"history"`
	OnGoingIssues      swDay           `json:"onGoingIssues"`
}

type swDetails struct {
	ID               int64         `json:"id"`
	DomainName       string        `json:"domainName"`
	Name             string        `json:"name"`
	Status           swStatus      `json:"status"`
	UnderMaintenance swFlag        `json:"underMaintenance"`
	Components       []swComponent `json:"components"`
}

type swStatus struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type swComponent struct {
	ID                int64         `json:"id"`
	Type              string        `json:"type"`
	Name              string        `json:"name"`
	Status            swStatus      `json:"status"`
	StatusMaintenance swStatus      `json:"statusMaintenance"`
	UnderMaintenance  swFlag        `json:"underMaintenance"`
	IsHidden          bool          `json:"isHidden"`
	Components        []swComponent `json:"components"`
}

// swFlag reads a flag the platform spells two ways: a component carries
// underMaintenance as a boolean, while the page around it carries the same
// field as the string "False".
type swFlag bool

func (f *swFlag) UnmarshalJSON(data []byte) error {
	switch strings.ToLower(strings.Trim(string(data), `"`)) {
	case "true":
		*f = true
	case "false":
		*f = false
	}
	return nil
}

type swDay struct {
	Date   string    `json:"date"`
	Issues []swIssue `json:"issues"`
}

type swIssue struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	CreatedAt    string `json:"createdAt"`
	ResolvedAt   string `json:"resolvedAt"`
	CurrentState string `json:"currentState"`
}

type swMaintenance struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	MaintenanceState string `json:"maintenanceState"`
}

func (s *Solarwinds) Fetch(ctx context.Context, t Target) (Report, error) {
	url := strings.TrimRight(t.URL, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Report{}, fmt.Errorf("solarwinds: build request: %w", err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return Report{}, fmt.Errorf("solarwinds: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Report{}, fmt.Errorf("solarwinds: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("solarwinds: %s returned HTTP %d", url, resp.StatusCode)
	}

	match := solarwindsNextData.FindSubmatch(body)
	if match == nil {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("solarwinds: %s carries no __NEXT_DATA__ script", url)
	}

	var payload swPayload
	if err := json.Unmarshal(match[1], &payload); err != nil {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("solarwinds: %s: decode __NEXT_DATA__: %w", url, err)
	}

	page := payload.Props.PageProps
	if page.Details.ID == 0 {
		return Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("solarwinds: %s is not a SolarWinds status page", url)
	}

	rep := Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        body,
	}
	// A page id is only unique inside one organisation, so the hash the
	// platform gives the page is the identity two vendors would collide on.
	if page.Details.DomainName != "" {
		rep.PageID = page.Details.DomainName
	} else {
		rep.PageID = strconv.FormatInt(page.Details.ID, 10)
	}

	var statuses []Health
	include := lowerSet(t.Include)
	position := 0

	// A group is a heading rather than a component of its own: it carries an
	// empty status and its children are what the page shows beneath it. A page
	// can also list components with no group around them.
	report := func(c swComponent, group string) {
		if c.ID == 0 || c.Name == "" || c.IsHidden {
			return
		}
		if len(include) > 0 && !include[strings.ToLower(strings.TrimSpace(c.Name))] {
			return
		}
		health := solarwindsComponentHealth(c)
		position++
		rep.Components = append(rep.Components, ComponentStatus{
			SourceID: strconv.FormatInt(c.ID, 10),
			Name:     c.Name,
			Group:    group,
			Position: position,
			Status:   health,
		})
		statuses = append(statuses, health)
	}

	for _, group := range page.Details.Components {
		if group.Type != "group" {
			report(group, "")
			continue
		}
		if group.IsHidden {
			continue
		}
		for _, child := range group.Components {
			report(child, group.Name)
		}
	}

	// The page states an overall state of its own, so that one is taken when the
	// adapter knows the word for it; only an unknown one falls back to the worst
	// component.
	var global Health
	if page.Details.UnderMaintenance {
		global = HealthMaintenance
	} else if health, ok := solarwindsHealth(page.Details.Status.Slug); ok {
		global = health
	} else {
		global = HealthUnknown
	}
	if global == HealthUnknown && len(statuses) > 0 {
		global = Worst(statuses...)
	}
	rep.Global = global

	rep.Incidents = solarwindsIncidents(page)

	return rep, nil
}

// solarwindsIncidents folds the sections the page keeps an incident in: the
// history, which is a day at a time, together with the one still open, and the
// maintenance that is planned or done. An incident carries no impact of its own
// here, so there is none to report.
func solarwindsIncidents(page swPageProps) []Incident {
	var out []Incident

	for _, day := range slices.Concat(page.History, []swDay{page.OnGoingIssues}) {
		for _, issue := range day.Issues {
			if issue.ID == 0 || issue.Title == "" {
				continue
			}
			item := Incident{
				SourceID:  "issue-" + strconv.FormatInt(issue.ID, 10),
				Name:      issue.Title,
				Status:    issue.CurrentState,
				StartedAt: solarwindsTime(issue.CreatedAt),
			}
			// An issue the page has not resolved carries the zero time here, so
			// it is left open rather than dated in year one.
			if resolved := solarwindsTime(issue.ResolvedAt); !resolved.IsZero() {
				item.ResolvedAt = &resolved
			}
			out = append(out, item)
		}
	}

	for _, window := range slices.Concat(page.Maintenance, page.OnGoingMaintenance) {
		if window.ID == 0 || window.Title == "" {
			continue
		}
		item := Incident{
			SourceID:  "maintenance-" + strconv.FormatInt(window.ID, 10),
			Name:      window.Title,
			Status:    window.MaintenanceState,
			StartedAt: solarwindsTime(window.StartTime),
		}
		// A planned window has an end time in the future, so only the one the
		// page calls completed is dated as finished.
		if strings.EqualFold(strings.TrimSpace(window.MaintenanceState), "completed") {
			if finished := solarwindsTime(window.EndTime); !finished.IsZero() {
				item.ResolvedAt = &finished
			}
		}
		out = append(out, item)
	}

	return out
}

// solarwindsComponentHealth reads the state of one component. A component that
// is under maintenance keeps its normal status, so the flag is what decides.
func solarwindsComponentHealth(c swComponent) Health {
	if c.UnderMaintenance {
		return HealthMaintenance
	}
	if health, ok := solarwindsHealth(c.Status.Slug); ok {
		return health
	}
	if health, ok := solarwindsHealth(c.StatusMaintenance.Slug); ok {
		return health
	}
	return HealthUnknown
}

func solarwindsHealth(slug string) (Health, bool) {
	health, ok := solarwindsState[strings.ToLower(strings.TrimSpace(slug))]
	return health, ok
}

func solarwindsTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func init() {
	Register(NewSolarwinds(nil))
}
