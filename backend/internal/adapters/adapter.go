package adapters

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
	SourceID  string
	Name      string
	Group     string
	Position  int
	Status    Health
	ChangedAt time.Time
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
	RetryAfter      time.Duration
	Raw             []byte
}

type Target struct {
	Slug                  string
	Name                  string
	URL                   string
	Category              string
	LogoURL               string
	Profile               string
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

// DefaultClient is what an adapter uses unless a test hands it another one: one
// timeout for every request, and a redirect limit so a page that points at
// itself cannot hold a poll open.
func DefaultClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

// RetryAfter reads how long a page asked to be left alone. The header carries
// either a number of seconds or a date, and zero means it said nothing usable,
// which leaves the wait to the poller.
func RetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}

	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}

	if at, err := http.ParseTime(value); err == nil {
		if wait := time.Until(at); wait > 0 {
			return wait
		}
	}
	return 0
}

// LowerSet turns the names a vendor includes into a lookup, so filtering a
// component is a map miss rather than a scan of the list. Nil means no filter.
func LowerSet(in []string) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[strings.ToLower(strings.TrimSpace(s))] = true
	}
	return out
}
