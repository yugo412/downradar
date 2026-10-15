package seed

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/catalog"
)

type vendorYAML struct {
	Slug                  string   `yaml:"slug"`
	Name                  string   `yaml:"name"`
	URL                   string   `yaml:"url"`
	Category              string   `yaml:"category"`
	LogoURL               string   `yaml:"logo_url"`
	Profile               string   `yaml:"profile"`
	Include               []string `yaml:"include"`
	Aliases               []string `yaml:"aliases"`
	SharedPage            bool     `yaml:"shared_page"`
	PollIntervalSeconds   *int     `yaml:"poll_interval_seconds"`
	ActiveIntervalSeconds *int     `yaml:"active_interval_seconds"`
}

type fileYAML struct {
	Vendors []vendorYAML `yaml:"vendors"`
}

// Load reads a bootstrap file of vendors and turns it into targets.
//
// Everything a vendor row needs is checked here, because the poller now trusts
// the table: an unknown profile, a duplicate slug or an internal url has to be
// rejected before it is written.
func Load(path string, profiles catalog.Profiles) ([]adapters.Target, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("seed: read %s: %w", path, err)
	}

	var f fileYAML
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("seed: parse %s: %w", path, err)
	}
	if len(f.Vendors) == 0 {
		return nil, fmt.Errorf("seed: %s contains no vendors", path)
	}

	targets := make([]adapters.Target, 0, len(f.Vendors))
	seen := make(map[string]bool, len(f.Vendors))

	for i, v := range f.Vendors {
		where := fmt.Sprintf("%s vendor #%d", path, i+1)
		if v.Slug != "" {
			where = fmt.Sprintf("%s vendor %q", path, v.Slug)
		}

		if v.Slug == "" || v.Name == "" || v.URL == "" {
			return nil, fmt.Errorf("seed: %s: slug, name and url are required", where)
		}
		if seen[v.Slug] {
			return nil, fmt.Errorf("seed: %s: duplicate slug", where)
		}
		seen[v.Slug] = true

		if err := catalog.ValidateURL(v.URL); err != nil {
			return nil, fmt.Errorf("seed: %s: %w", where, err)
		}

		if v.Profile == "" {
			return nil, fmt.Errorf("seed: %s: profile is required, it decides which adapter polls it", where)
		}
		profile, ok := profiles[v.Profile]
		if !ok {
			return nil, fmt.Errorf("seed: %s: profile %q is not declared in services.yaml", where, v.Profile)
		}

		t := adapters.Target{
			Slug:                  v.Slug,
			Name:                  v.Name,
			URL:                   v.URL,
			Category:              v.Category,
			LogoURL:               v.LogoURL,
			Profile:               v.Profile,
			Kind:                  profile.Kind,
			Endpoint:              profile.Endpoint,
			Include:               v.Include,
			Aliases:               v.Aliases,
			AllowSharedPage:       v.SharedPage,
			PollIntervalSeconds:   profile.PollIntervalSeconds,
			ActiveIntervalSeconds: profile.ActiveIntervalSeconds,
			Spec:                  profile.Spec,
		}
		if v.PollIntervalSeconds != nil {
			t.PollIntervalSeconds = *v.PollIntervalSeconds
		}
		if v.ActiveIntervalSeconds != nil {
			t.ActiveIntervalSeconds = *v.ActiveIntervalSeconds
		}
		if t.PollIntervalSeconds <= 0 || t.ActiveIntervalSeconds <= 0 {
			return nil, fmt.Errorf("seed: %s: poll intervals must be greater than zero", where)
		}

		targets = append(targets, t)
	}
	return targets, nil
}
