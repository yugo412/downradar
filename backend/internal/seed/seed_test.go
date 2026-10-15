package seed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"downradar.info/backend/internal/catalog"
)

func profiles(t *testing.T) catalog.Profiles {
	t.Helper()
	return catalog.Profiles{
		"statuspage-v2": {
			Kind:                  "statuspage",
			Endpoint:              "/api/v2/summary.json",
			PollIntervalSeconds:   300,
			ActiveIntervalSeconds: 60,
		},
		"slack": {
			Kind:                  "slack",
			PollIntervalSeconds:   900,
			ActiveIntervalSeconds: 120,
		},
	}
}

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vendors.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// seedFilePath walks up until it finds the bootstrap file, so the test works
// both on the host and inside the container where only part of the repo is
// mounted.
func seedFilePath(t *testing.T) string {
	t.Helper()

	if p := os.Getenv("SEED_FILE"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "seed", "vendors.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("seed/vendors.yaml not found in this directory or any parent")
	return ""
}

// servicesPath finds the profile file the same way the API does.
func servicesPath(t *testing.T) string {
	t.Helper()

	if p := os.Getenv("SERVICES_FILE"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "services.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("services.yaml not found in this directory or any parent")
	return ""
}

func TestLoadRealSeedFile(t *testing.T) {
	// The real profiles, not fixtures: the seed file references every profile
	// that services.yaml declares, and a mismatch should fail here.
	realProfiles, err := catalog.Load(servicesPath(t))
	if err != nil {
		t.Fatalf("load profiles: %v", err)
	}

	targets, err := Load(seedFilePath(t), realProfiles)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(targets) < 50 {
		t.Fatalf("only %d vendors parsed, file looks broken", len(targets))
	}

	seen := map[string]bool{}
	for _, tg := range targets {
		if seen[tg.Slug] {
			t.Errorf("duplicate slug: %s", tg.Slug)
		}
		seen[tg.Slug] = true

		if tg.Profile == "" {
			t.Errorf("%s: no profile", tg.Slug)
		}
		if tg.Kind == "" {
			t.Errorf("%s: no kind resolved from its profile", tg.Slug)
		}
		if tg.PollIntervalSeconds <= 0 || tg.ActiveIntervalSeconds <= 0 {
			t.Errorf("%s: invalid interval", tg.Slug)
		}
	}

	// sendgrid and twilio share a status page, so both must declare it.
	for _, slug := range []string{"sendgrid", "twilio"} {
		found := false
		for _, tg := range targets {
			if tg.Slug == slug {
				found = true
				if !tg.AllowSharedPage {
					t.Errorf("%s should declare shared_page", slug)
				}
			}
		}
		if !found {
			t.Errorf("%s missing from the seed file", slug)
		}
	}
}

func TestLoadInheritsProfileSettings(t *testing.T) {
	body := `
vendors:
  - slug: a
    name: A
    url: https://a.example.com
    profile: statuspage-v2
  - slug: b
    name: B
    url: https://b.example.com
    profile: statuspage-v2
    poll_interval_seconds: 30
`
	targets, err := Load(writeTemp(t, body), profiles(t))
	if err != nil {
		t.Fatal(err)
	}
	if targets[0].Kind != "statuspage" || targets[0].Endpoint != "/api/v2/summary.json" {
		t.Errorf("vendor a did not inherit its profile: %+v", targets[0])
	}
	if targets[0].PollIntervalSeconds != 300 {
		t.Errorf("vendor a interval = %d, want the profile's 300", targets[0].PollIntervalSeconds)
	}
	if targets[1].PollIntervalSeconds != 30 {
		t.Errorf("per-vendor override not applied: %d", targets[1].PollIntervalSeconds)
	}
	if targets[1].ActiveIntervalSeconds != 60 {
		t.Errorf("active interval not inherited: %d", targets[1].ActiveIntervalSeconds)
	}
}

func TestLoadRejectsBadVendors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "duplicate slug",
			body: `
vendors:
  - {slug: a, name: A, url: "https://a.example.com", profile: statuspage-v2}
  - {slug: a, name: B, url: "https://b.example.com", profile: statuspage-v2}
`,
			want: "duplicate slug",
		},
		{
			name: "no profile",
			body: `
vendors:
  - {slug: a, name: A, url: "https://a.example.com"}
`,
			want: "profile is required",
		},
		{
			name: "unknown profile",
			body: `
vendors:
  - {slug: a, name: A, url: "https://a.example.com", profile: gone}
`,
			want: "not declared",
		},
		{
			name: "internal url",
			body: `
vendors:
  - {slug: a, name: A, url: "http://127.0.0.1:8080", profile: statuspage-v2}
`,
			want: "internal",
		},
		{
			name: "missing name",
			body: `
vendors:
  - {slug: a, url: "https://a.example.com", profile: statuspage-v2}
`,
			want: "required",
		},
		{
			name: "unknown field",
			body: `
vendors:
  - {slug: a, name: A, url: "https://a.example.com", profile: statuspage-v2, slugh: typo}
`,
			want: "field",
		},
		{
			name: "no vendors",
			body: "vendors: []\n",
			want: "no vendors",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeTemp(t, tc.body), profiles(t))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}
