package catalog

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"downradar.info/backend/internal/adapters"
)

type profile struct {
	Kind                  string         `yaml:"kind"`
	Endpoint              string         `yaml:"endpoint"`
	PollIntervalSeconds   int            `yaml:"poll_interval_seconds"`
	ActiveIntervalSeconds int            `yaml:"active_interval_seconds"`
	Spec                  map[string]any `yaml:"spec"`
}

type vendorYAML struct {
	Slug                  string         `yaml:"slug"`
	Name                  string         `yaml:"name"`
	URL                   string         `yaml:"url"`
	Category              string         `yaml:"category"`
	LogoURL               string         `yaml:"logo_url"`
	Profile               string         `yaml:"profile"`
	Kind                  string         `yaml:"kind"`
	Endpoint              string         `yaml:"endpoint"`
	Include               []string       `yaml:"include"`
	Aliases               []string       `yaml:"aliases"`
	PollIntervalSeconds   *int           `yaml:"poll_interval_seconds"`
	ActiveIntervalSeconds *int           `yaml:"active_interval_seconds"`
	Spec                  map[string]any `yaml:"spec"`
	Enabled               *bool          `yaml:"enabled"`
}

type fileYAML struct {
	Profiles map[string]profile `yaml:"profiles"`
	Vendors  []vendorYAML       `yaml:"vendors"`
}

const (
	defaultPollInterval   = 300
	defaultActiveInterval = 60
)

func Load(path string) ([]adapters.Target, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("catalog: read %s: %w", path, err)
	}

	var f fileYAML
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("catalog: parse %s: %w", path, err)
	}
	if len(f.Vendors) == 0 {
		return nil, fmt.Errorf("catalog: %s contains no vendors", path)
	}

	targets := make([]adapters.Target, 0, len(f.Vendors))
	seen := make(map[string]bool, len(f.Vendors))

	for i, v := range f.Vendors {
		where := fmt.Sprintf("%s vendor #%d", path, i+1)
		if v.Slug != "" {
			where = fmt.Sprintf("%s vendor %q", path, v.Slug)
		}

		if v.Slug == "" || v.Name == "" || v.URL == "" {
			return nil, fmt.Errorf("catalog: %s: slug, name and url are required", where)
		}
		if seen[v.Slug] {
			return nil, fmt.Errorf("catalog: %s: duplicate slug", where)
		}
		seen[v.Slug] = true

		if v.Enabled != nil && !*v.Enabled {
			continue
		}

		if err := validateURL(v.URL); err != nil {
			return nil, fmt.Errorf("catalog: %s: %w", where, err)
		}

		t := adapters.Target{
			Slug:                  v.Slug,
			Name:                  v.Name,
			URL:                   v.URL,
			Category:              v.Category,
			LogoURL:               v.LogoURL,
			Kind:                  v.Kind,
			Endpoint:              v.Endpoint,
			Include:               v.Include,
			Aliases:               v.Aliases,
			PollIntervalSeconds:   defaultPollInterval,
			ActiveIntervalSeconds: defaultActiveInterval,
			Spec:                  v.Spec,
		}

		if v.Profile != "" {
			p, ok := f.Profiles[v.Profile]
			if !ok {
				return nil, fmt.Errorf("catalog: %s: profile %q not found in profiles", where, v.Profile)
			}
			if t.Kind == "" {
				t.Kind = p.Kind
			}
			if t.Endpoint == "" {
				t.Endpoint = p.Endpoint
			}
			if t.Spec == nil {
				t.Spec = p.Spec
			}
			if p.PollIntervalSeconds > 0 {
				t.PollIntervalSeconds = p.PollIntervalSeconds
			}
			if p.ActiveIntervalSeconds > 0 {
				t.ActiveIntervalSeconds = p.ActiveIntervalSeconds
			}
		}

		if v.PollIntervalSeconds != nil {
			t.PollIntervalSeconds = *v.PollIntervalSeconds
		}
		if v.ActiveIntervalSeconds != nil {
			t.ActiveIntervalSeconds = *v.ActiveIntervalSeconds
		}

		if t.Kind == "" {
			return nil, fmt.Errorf("catalog: %s: kind is not set and no profile provides it", where)
		}
		if _, ok := adapters.Get(t.Kind); !ok {
			return nil, fmt.Errorf("catalog: %s: adapter %q is not registered (available: %s)",
				where, t.Kind, strings.Join(adapters.Kinds(), ", "))
		}
		if t.PollIntervalSeconds <= 0 || t.ActiveIntervalSeconds <= 0 {
			return nil, fmt.Errorf("catalog: %s: poll intervals must be greater than zero", where)
		}

		targets = append(targets, t)
	}

	return targets, nil
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q is not supported (http/https only)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}

	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") ||
		strings.HasSuffix(lower, ".internal") {
		return fmt.Errorf("host %q is not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified()) {
		return fmt.Errorf("host %q is an internal address and is not allowed", host)
	}
	return nil
}
