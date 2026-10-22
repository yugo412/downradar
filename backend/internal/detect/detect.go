// Package detect answers which adapter profile reads a status page. It is what
// lets a vendor be added before its platform is known: the profiles in the queue
// are tried in order, and the first one whose adapter can read the page wins.
package detect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/catalog"
)

// attemptTimeout bounds one profile's fetch. The queue tries every profile in
// turn, so the worst case is this times its length; a page that needs longer is
// not answering, and giving up keeps one dead profile from spending the request.
const attemptTimeout = 6 * time.Second

// Detector probes the profiles in the order services.yaml declares.
type Detector struct {
	candidates []catalog.Candidate
}

func New(cat catalog.Catalog) *Detector {
	return &Detector{candidates: cat.Candidates()}
}

// Match is the profile that read the page, together with what it read. The
// report is handed on so the page is not fetched a second time.
type Match struct {
	Profile string
	Report  adapters.Report
}

// Attempt is what one profile made of the page.
type Attempt struct {
	Profile string
	Err     error
}

// NoMatch is returned when the queue ran out of profiles. It carries the reason
// each one gave, which is what tells an operator whether the platform deserves
// an adapter of its own or the url is simply wrong.
type NoMatch struct {
	URL      string
	Attempts []Attempt
}

func (e *NoMatch) Error() string {
	if len(e.Attempts) == 0 {
		return fmt.Sprintf("no profile matched %s: the detect queue is empty", e.URL)
	}

	reasons := make([]string, 0, len(e.Attempts))
	for _, a := range e.Attempts {
		reasons = append(reasons, fmt.Sprintf("%s: %v", a.Profile, a.Err))
	}
	return fmt.Sprintf("no profile matched %s (%s)", e.URL, strings.Join(reasons, "; "))
}

// Detect walks the queue in order and stops at the first profile that reads the
// page.
func (d *Detector) Detect(ctx context.Context, name, pageURL string) (Match, error) {
	if len(d.candidates) == 0 {
		return Match{}, &NoMatch{URL: pageURL}
	}

	attempts := make([]Attempt, 0, len(d.candidates))
	for _, c := range d.candidates {
		report, err := d.probe(ctx, c, name, pageURL)
		if err == nil {
			return Match{Profile: c.Name, Report: report}, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Match{}, ctxErr
		}
		attempts = append(attempts, Attempt{Profile: c.Name, Err: err})
	}

	return Match{}, &NoMatch{URL: pageURL, Attempts: attempts}
}

func (d *Detector) probe(ctx context.Context, c catalog.Candidate, name, pageURL string) (adapters.Report, error) {
	adapter, ok := adapters.Get(c.Profile.Kind)
	if !ok {
		return adapters.Report{}, fmt.Errorf("adapter %q is not registered", c.Profile.Kind)
	}

	attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()

	return adapter.Fetch(attemptCtx, adapters.Target{
		Name:                  name,
		URL:                   pageURL,
		Profile:               c.Name,
		Kind:                  c.Profile.Kind,
		Endpoint:              c.Profile.Endpoint,
		PollIntervalSeconds:   c.Profile.PollIntervalSeconds,
		ActiveIntervalSeconds: c.Profile.ActiveIntervalSeconds,
		Spec:                  c.Profile.Spec,
	})
}
