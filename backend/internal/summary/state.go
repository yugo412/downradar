package summary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/store"
)

const (
	maxComponents   = 25
	maxChanges      = 10
	maxIncidents    = 10
	maxSummaryRunes = 600
	timeFormat      = time.RFC3339
)

// state is the part of a vendor page the writer is allowed to see. It is built
// from the stored state, not from the rendered page, so the same input always
// produces the same fingerprint and an unchanged vendor is not written twice.
type state struct {
	Vendor              string         `json:"vendor"`
	SourceURL           string         `json:"sourceUrl"`
	Status              string         `json:"status"`
	StatusSince         string         `json:"statusSince,omitempty"`
	ComponentsTotal     int            `json:"componentsTotal"`
	ComponentsOK        int            `json:"componentsOperational"`
	Components          []component    `json:"componentsNeedingAttention,omitempty"`
	ComponentsOmitted   int            `json:"componentsNeedingAttentionOmitted,omitempty"`
	OpenIncidents       []incident     `json:"openIncidents,omitempty"`
	RecentStatusChanges []statusChange `json:"recentStatusChanges,omitempty"`
}

type component struct {
	Name   string `json:"name"`
	Group  string `json:"group,omitempty"`
	Status string `json:"status"`
	Since  string `json:"since,omitempty"`
}

type incident struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Impact  string `json:"impact,omitempty"`
	Started string `json:"startedAt,omitempty"`
}

type statusChange struct {
	Component string `json:"component,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to"`
	At        string `json:"at"`
}

func buildState(v *store.VendorDetail) state {
	s := state{
		Vendor:          v.Name,
		SourceURL:       v.URL,
		Status:          v.Status,
		StatusSince:     formatTime(v.ChangedAt),
		ComponentsTotal: len(v.ComponentsList),
	}

	for _, c := range v.ComponentsList {
		if c.Status == string(adapters.HealthOK) {
			s.ComponentsOK++
			continue
		}
		if len(s.Components) >= maxComponents {
			s.ComponentsOmitted++
			continue
		}
		s.Components = append(s.Components, component{
			Name:   c.Name,
			Group:  c.Group,
			Status: c.Status,
			Since:  formatTime(c.ChangedAt),
		})
	}

	for _, i := range v.Incidents {
		if i.ResolvedAt != nil {
			continue
		}
		if len(s.OpenIncidents) >= maxIncidents {
			break
		}
		s.OpenIncidents = append(s.OpenIncidents, incident{
			Name:    i.Name,
			Status:  i.Status,
			Impact:  i.Impact,
			Started: formatTime(i.StartedAt),
		})
	}

	// A baseline event has no previous status and says nothing about a change.
	// The current component state still carries when it began.
	for _, e := range v.RecentEvents {
		if e.Previous == "" {
			continue
		}
		if len(s.RecentStatusChanges) >= maxChanges {
			break
		}
		s.RecentStatusChanges = append(s.RecentStatusChanges, statusChange{
			Component: e.Component,
			From:      e.Previous,
			To:        e.Status,
			At:        e.ChangedAt.UTC().Format(timeFormat),
		})
	}

	return s
}

// worthSummarizing says whether the official state has anything a reader would
// call a problem. A fully operational vendor has no summary, and neither does a
// row that has not been read yet and has no components or incidents to discuss.
func (s state) worthSummarizing() bool {
	if len(s.OpenIncidents) > 0 {
		return true
	}
	if len(s.Components) > 0 {
		return true
	}
	if s.Status != string(adapters.HealthOK) {
		return s.Status != string(adapters.HealthUnknown) ||
			s.ComponentsTotal > 0 || len(s.RecentStatusChanges) > 0
	}
	return false
}

func (s state) fingerprint() string {
	data, err := json.Marshal(s)
	if err != nil {
		// The struct is built from strings, numbers and slices, so this cannot
		// fail in practice. A stable empty value keeps the caller safe if it does.
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s state) prompt(now time.Time) (string, string) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		data = []byte("{}")
	}

	system := `You write a short status summary for a public service status aggregator.
Use only the JSON state supplied. Do not invent causes, fixes, ETAs or facts that are not in it.
Write one or two plain-text sentences in English. Say what changed, which components are affected, and since when.
If an open incident is present, mention it. Do not use markdown, headings, lists or quotation marks.
Do not repeat the vendor name unless the sentence needs it. Do not give advice.
If the state shows nothing wrong, return an empty string.`

	user := fmt.Sprintf("Current time: %s\nVendor state:\n%s",
		now.UTC().Format(timeFormat), data)
	return system, user
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(timeFormat)
}
