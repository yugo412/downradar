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

// Profile describes how one platform is polled. Vendors reference a profile by
// name; which vendors exist is a database question.
type Profile struct {
	Kind                  string
	Endpoint              string
	PollIntervalSeconds   int
	ActiveIntervalSeconds int
	Spec                  map[string]any
}

type Profiles map[string]Profile

type profileYAML struct {
	Kind                  string         `yaml:"kind"`
	Endpoint              string         `yaml:"endpoint"`
	PollIntervalSeconds   int            `yaml:"poll_interval_seconds"`
	ActiveIntervalSeconds int            `yaml:"active_interval_seconds"`
	Spec                  map[string]any `yaml:"spec"`
}

type fileYAML struct {
	Profiles map[string]profileYAML `yaml:"profiles"`
}

// Load reads services.yaml, which now only holds profiles.
//
// Validation is deliberately strict and fails at startup: a profile naming an
// adapter that does not exist would otherwise turn into vendors that silently
// never poll.
func Load(path string) (Profiles, error) {
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
	if len(f.Profiles) == 0 {
		return nil, fmt.Errorf("catalog: %s declares no profiles", path)
	}

	out := make(Profiles, len(f.Profiles))
	for name, p := range f.Profiles {
		if p.Kind == "" {
			return nil, fmt.Errorf("catalog: profile %q has no kind", name)
		}
		if _, ok := adapters.Get(p.Kind); !ok {
			return nil, fmt.Errorf("catalog: profile %q uses adapter %q, which is not registered (available: %s)",
				name, p.Kind, strings.Join(adapters.Kinds(), ", "))
		}
		if p.PollIntervalSeconds <= 0 || p.ActiveIntervalSeconds <= 0 {
			return nil, fmt.Errorf("catalog: profile %q must have positive poll intervals", name)
		}

		out[name] = Profile{
			Kind:                  p.Kind,
			Endpoint:              p.Endpoint,
			PollIntervalSeconds:   p.PollIntervalSeconds,
			ActiveIntervalSeconds: p.ActiveIntervalSeconds,
			Spec:                  p.Spec,
		}
	}
	return out, nil
}

// ValidateURL rejects schemes other than http/https and hosts on the internal
// network. It used to guard services.yaml; now it guards whatever writes a
// vendor row, so that a bad url cannot reach the poller.
func ValidateURL(raw string) error {
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
