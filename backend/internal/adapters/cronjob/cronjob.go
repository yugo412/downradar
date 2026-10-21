package cronjob

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"time"

	"downradar.info/backend/internal/adapters"
)

const KindCronJob = "cronjob"

const defaultCronJobAPI = "https://api.cron-job.org/"

type CronJob struct {
	client *http.Client
}

func NewCronJob(client *http.Client) *CronJob {
	if client == nil {
		client = adapters.DefaultClient()
	}
	return &CronJob{client: client}
}

func (c *CronJob) Kind() string { return KindCronJob }

type cjResponse struct {
	StatusPage cjStatusPage `json:"statusPage"`
}

type cjStatusPage struct {
	Meta     cjMeta      `json:"statusPageMeta"`
	Monitors []cjMonitor `json:"statusPageMonitors"`
}

type cjMeta struct {
	Title string `json:"title"`
}

type cjMonitor struct {
	Title      string       `json:"title"`
	Enabled    bool         `json:"enabled"`
	Percentile float64      `json:"percentile"`
	Thresholds cjThresholds `json:"thresholds"`
	Series     cjSeries     `json:"timeSeriesData"`
}

type cjThresholds struct {
	Uptime  cjLimit `json:"uptime"`
	Latency cjLimit `json:"latency"`
}

type cjLimit struct {
	Error   float64 `json:"error"`
	Warning float64 `json:"warning"`
}

type cjSeries struct {
	Last24Hours [][]float64 `json:"last24Hours"`
}

func (c *CronJob) Fetch(ctx context.Context, t adapters.Target) (adapters.Report, error) {
	apiURL := t.Endpoint
	if apiURL == "" {
		apiURL = defaultCronJobAPI
	}

	domain, err := pageDomain(t.URL)
	if err != nil {
		return adapters.Report{}, err
	}

	payload, err := json.Marshal(map[string]string{"domain": domain})
	if err != nil {
		return adapters.Report{}, fmt.Errorf("cronjob: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return adapters.Report{}, fmt.Errorf("cronjob: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Method", "GetPublicStatusPage")
	req.Header.Set("X-UI-Language", "en")
	req.Header.Set("User-Agent", adapters.UserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return adapters.Report{}, fmt.Errorf("cronjob: request %s: %w", apiURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return adapters.Report{}, fmt.Errorf("cronjob: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.Report{HTTPStatus: resp.StatusCode, RetryAfter: adapters.RetryAfter(resp), Raw: body},
			fmt.Errorf("cronjob: %s for domain %s returned HTTP %d", apiURL, domain, resp.StatusCode)
	}

	var parsed cjResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("cronjob: decode response for %s: %w", domain, err)
	}
	if len(parsed.StatusPage.Monitors) == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("cronjob: %s reports no monitors", domain)
	}

	rep := adapters.Report{
		FetchedAt:  time.Now().UTC(),
		HTTPStatus: resp.StatusCode,
		Raw:        body,
	}

	statuses := make([]adapters.Health, 0, len(parsed.StatusPage.Monitors))
	position := 0
	for _, m := range parsed.StatusPage.Monitors {
		if !m.Enabled {
			continue
		}
		position++

		buckets := lastBuckets(m.Series.Last24Hours, 2)
		uptime := meanOf(uptimeRatios(buckets))
		latency := percentileOf(latencies(buckets), m.Percentile)

		health := cronjobHealth(m.Thresholds, uptime, latency)
		rep.Components = append(rep.Components, adapters.ComponentStatus{
			SourceID: m.Title,
			Name:     m.Title,
			Position: position,
			Status:   health,
		})
		statuses = append(statuses, health)
	}

	if len(rep.Components) == 0 {
		return adapters.Report{HTTPStatus: resp.StatusCode, Raw: body},
			fmt.Errorf("cronjob: %s has no enabled monitors", domain)
	}

	rep.Global = adapters.Worst(statuses...)
	return rep, nil
}

func cronjobHealth(th cjThresholds, uptime, latency float64) adapters.Health {
	health := adapters.HealthUnknown

	if uptime >= 0 {
		switch {
		case th.Uptime.Error > 0 && uptime <= th.Uptime.Error:
			health = adapters.HealthMajor
		case th.Uptime.Warning > 0 && uptime <= th.Uptime.Warning:
			health = adapters.HealthPartial
		default:
			health = adapters.HealthOK
		}
	}

	if latency >= 0 {
		latencyHealth := adapters.HealthOK
		switch {
		case th.Latency.Error > 0 && latency >= th.Latency.Error:
			latencyHealth = adapters.HealthDegraded
		case th.Latency.Warning > 0 && latency >= th.Latency.Warning:
			latencyHealth = adapters.HealthDegraded
		}
		health = adapters.Worst(health, latencyHealth)
	}

	return health
}

func pageDomain(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("cronjob: parse url %q: %w", raw, err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("cronjob: url %q has no host", raw)
	}
	return host, nil
}

func lastBuckets(buckets [][]float64, n int) [][]float64 {
	if len(buckets) <= n {
		return buckets
	}
	return buckets[len(buckets)-n:]
}

func uptimeRatios(buckets [][]float64) []float64 {
	out := make([]float64, 0, len(buckets))
	for _, b := range buckets {
		if len(b) < 4 || b[3] <= 0 {
			continue
		}
		out = append(out, b[2]/b[3])
	}
	return out
}

func latencies(buckets [][]float64) []float64 {
	out := make([]float64, 0, len(buckets))
	for _, b := range buckets {
		if len(b) < 2 || b[1] < 0 {
			continue
		}
		out = append(out, b[1])
	}
	return out
}

func meanOf(values []float64) float64 {
	if len(values) == 0 {
		return -1
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func percentileOf(values []float64, p float64) float64 {
	if len(values) == 0 {
		return -1
	}
	if len(values) == 1 {
		return values[0]
	}

	sorted := slices.Clone(values)
	slices.Sort(sorted)

	r := math.Max(0, float64(len(sorted))*p-1)
	lower := int(math.Floor(r))
	if lower >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	if math.Abs(float64(lower)-r) < 1e-5 {
		return sorted[lower]
	}
	upper := int(math.Ceil(r))
	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	base := sorted[lower]
	return base + (sorted[upper]-base)*(r-math.Floor(r))
}

func init() {
	adapters.Register(NewCronJob(nil))
}
