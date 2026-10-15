package adapters

import (
	"context"
	"time"
)

const UserAgent = "downradar.info/0.1 (+https://downradar.info; status aggregator)"

type Health string

const (
	HealthOK          Health = "ok"
	HealthDegraded    Health = "degraded"
	HealthPartial     Health = "partial"
	HealthMajor       Health = "major"
	HealthMaintenance Health = "maintenance"
	HealthUnknown     Health = "unknown"
)

func (h Health) Healthy() bool {
	return h == HealthOK || h == HealthMaintenance
}

func (h Health) Severity() int {
	switch h {
	case HealthMajor:
		return 5
	case HealthPartial:
		return 4
	case HealthDegraded:
		return 3
	case HealthUnknown:
		return 2
	case HealthMaintenance:
		return 1
	case HealthOK:
		return 0
	default:
		return 2
	}
}

type ComponentStatus struct {
	SourceID string
	Name     string
	Group    string
	Position int
	Status   Health
}

type Incident struct {
	SourceID   string
	Name       string
	Status     string
	Impact     string
	StartedAt  time.Time
	ResolvedAt *time.Time
	URL        string
}

type Report struct {
	Global          Health
	Components      []ComponentStatus
	Incidents       []Incident
	PageID          string
	FetchedAt       time.Time
	SourceUpdatedAt time.Time
	HTTPStatus      int
	Raw             []byte
}

type Target struct {
	Slug                  string
	Name                  string
	URL                   string
	Category              string
	LogoURL               string
	Kind                  string
	Endpoint              string
	Include               []string
	Aliases               []string
	AllowSharedPage       bool
	PollIntervalSeconds   int
	ActiveIntervalSeconds int
	Spec                  map[string]any
}

type Adapter interface {
	Kind() string
	Fetch(ctx context.Context, t Target) (Report, error)
}

func Worst(hs ...Health) Health {
	if len(hs) == 0 {
		return HealthUnknown
	}
	worst := hs[0]
	for _, h := range hs[1:] {
		if h.Severity() > worst.Severity() {
			worst = h
		}
	}
	return worst
}
