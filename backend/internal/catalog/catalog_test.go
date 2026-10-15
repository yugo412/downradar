package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func servicesFilePath(t *testing.T) string {
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

func TestLoadRealServicesFile(t *testing.T) {
	targets, err := Load(servicesFilePath(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(targets) < 10 {
		t.Fatalf("only %d vendors parsed, file looks broken", len(targets))
	}

	seen := map[string]bool{}
	for _, tg := range targets {
		if seen[tg.Slug] {
			t.Errorf("duplicate slug: %s", tg.Slug)
		}
		seen[tg.Slug] = true

		if tg.Kind == "" {
			t.Errorf("%s: empty kind", tg.Slug)
		}
		if tg.PollIntervalSeconds <= 0 || tg.ActiveIntervalSeconds <= 0 {
			t.Errorf("%s: invalid interval", tg.Slug)
		}
		if tg.ActiveIntervalSeconds > tg.PollIntervalSeconds {
			t.Errorf("%s: active interval (%d) is slower than normal interval (%d)",
				tg.Slug, tg.ActiveIntervalSeconds, tg.PollIntervalSeconds)
		}
	}
}

func TestLoadRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "duplicate slug",
			body: `
vendors:
  - {slug: a, name: A, url: "https://a.example.com", kind: statuspage}
  - {slug: a, name: B, url: "https://b.example.com", kind: statuspage}
`,
			want: "duplicate",
		},
		{
			name: "unregistered kind",
			body: `
vendors:
  - {slug: a, name: A, url: "https://a.example.com", kind: nope}
`,
			want: "not registered",
		},
		{
			name: "unknown field",
			body: `
vendors:
  - {slug: a, name: A, url: "https://a.example.com", kind: statuspage, slugh: typo}
`,
			want: "field",
		},
		{
			name: "missing profile",
			body: `
vendors:
  - {slug: a, name: A, url: "https://a.example.com", profile: gone}
`,
			want: "profile",
		},
		{
			name: "internal url",
			body: `
vendors:
  - {slug: a, name: A, url: "http://127.0.0.1:8080", kind: statuspage}
`,
			want: "internal",
		},
		{
			name: "unsupported scheme",
			body: `
vendors:
  - {slug: a, name: A, url: "file:///etc/passwd", kind: statuspage}
`,
			want: "scheme",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "services.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestProfileInheritance(t *testing.T) {
	body := `
profiles:
  sp:
    kind: statuspage
    endpoint: /api/v2/summary.json
    poll_interval_seconds: 900
    active_interval_seconds: 120
vendors:
  - slug: a
    name: A
    url: https://a.example.com
    profile: sp
  - slug: b
    name: B
    url: https://b.example.com
    profile: sp
    poll_interval_seconds: 30
`
	dir := t.TempDir()
	path := filepath.Join(dir, "services.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	targets, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if targets[0].Kind != "statuspage" || targets[0].PollIntervalSeconds != 900 {
		t.Errorf("vendor a did not inherit profile: %+v", targets[0])
	}
	if targets[1].PollIntervalSeconds != 30 {
		t.Errorf("per-vendor override not applied: %d", targets[1].PollIntervalSeconds)
	}
	if targets[1].ActiveIntervalSeconds != 120 {
		t.Errorf("active interval not inherited: %d", targets[1].ActiveIntervalSeconds)
	}
}
