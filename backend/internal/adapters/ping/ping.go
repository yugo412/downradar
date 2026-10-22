package ping

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindPing = "ping"

// Ping watches a web address by asking for it; the answer, or its absence, is
// the whole report. It is chosen by hand for a service with no status page,
// never detected, because it would answer for any url that is up.
type Ping struct {
	client *http.Client
}

func NewPing(client *http.Client) *Ping {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &Ping{client: client}
}

func (p *Ping) Kind() string { return KindPing }

// pingTarget is the address that has to answer, and the name the component
// carries: the host, since that is the thing being watched.
type pingTarget struct {
	url  string
	name string
}

func parsePingTarget(raw string) (pingTarget, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return pingTarget{}, fmt.Errorf("ping: parse url %q: %w", raw, err)
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return pingTarget{}, fmt.Errorf("ping: %q is not an http address", raw)
	}
	return pingTarget{url: u.String(), name: u.Host}, nil
}

// Fetch watches the target. A request that does not come back is the finding,
// not a failure: reachability is all this profile reports, so no answer is an
// outage rather than a reason to give up on the vendor.
func (p *Ping) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	target, err := parsePingTarget(t.URL)
	if err != nil {
		return adapters.Report{}, err
	}

	rep, err := p.request(ctx, target)
	if err != nil {
		return pingReport(target, adapters.HealthMajor, 0), nil
	}
	return rep, nil
}

// request asks the target for itself and reads only the answer. The body is left
// unread: a ping is about whether the page is there, not about what it says.
func (p *Ping) request(ctx context.Context, target pingTarget) (adapters.Report, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("ping: build request: %w", err)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("ping: request %s: %w", target.url, err)
	}
	defer resp.Body.Close()

	return pingReport(target, pingHealth(resp.StatusCode), resp.StatusCode), nil
}

func pingReport(target pingTarget, health adapters.Health, status int) adapters.Report {
	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: status,
		Global:     health,
	}
	rep.Components = []adapters.ComponentStatus{{
		SourceID: target.name,
		Name:     target.name,
		Position: 1,
		Status:   health,
	}}
	return rep
}

// pingHealth maps the answer onto the states the system speaks. Anything that
// answers is working, and a 5xx is broken. A refusal or a missing page says
// nothing about the service, so it stays unknown; the fetch log has the code.
func pingHealth(code int) adapters.Health {
	switch {
	case code >= 200 && code < 400:
		return adapters.HealthOK
	case code >= 500:
		return adapters.HealthMajor
	default:
		return adapters.HealthUnknown
	}
}

func init() {
	adapters.Register(NewPing(nil))
}
