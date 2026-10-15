package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// servicesFilePath prefers SERVICES_FILE, which is what the running API uses,
// and falls back to walking up from the working directory. The two environments
// differ: on the host the file sits at the repository root, while in the
// container it is mounted at a path only the environment variable knows about.
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

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "services.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRealProfiles(t *testing.T) {
	profiles, err := Load(servicesFilePath(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(profiles) < 4 {
		t.Fatalf("only %d profiles parsed, file looks broken", len(profiles))
	}

	for name, p := range profiles {
		if p.Kind == "" {
			t.Errorf("%s: empty kind", name)
		}
		if p.PollIntervalSeconds <= 0 || p.ActiveIntervalSeconds <= 0 {
			t.Errorf("%s: invalid interval", name)
		}
		if p.ActiveIntervalSeconds > p.PollIntervalSeconds {
			t.Errorf("%s: active interval (%d) is slower than normal interval (%d)",
				name, p.ActiveIntervalSeconds, p.PollIntervalSeconds)
		}
	}

	// The registry must not creep back into this file.
	if strings.Contains(strings.ToLower(readFile(t, servicesFilePath(t))), "\nvendors:") {
		t.Error("services.yaml still declares vendors, they belong in the table now")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoadRejectsBadProfiles(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "no profiles",
			body: "profiles: {}\n",
			want: "no profiles",
		},
		{
			name: "unknown kind",
			body: `
profiles:
  a: {kind: nope, poll_interval_seconds: 300, active_interval_seconds: 60}
`,
			want: "not registered",
		},
		{
			name: "missing kind",
			body: `
profiles:
  a: {poll_interval_seconds: 300, active_interval_seconds: 60}
`,
			want: "no kind",
		},
		{
			name: "zero interval",
			body: `
profiles:
  a: {kind: statuspage, poll_interval_seconds: 0, active_interval_seconds: 60}
`,
			want: "positive poll intervals",
		},
		{
			name: "unknown field",
			body: `
profiles:
  a: {kind: statuspage, poll_interval_seconds: 300, active_interval_seconds: 60, slugh: typo}
`,
			want: "field",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeTemp(t, tc.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	ok := []string{
		"https://status.example.com",
		"http://status.example.com/api",
		"https://status.example.com:8443/x",
	}
	for _, raw := range ok {
		if err := ValidateURL(raw); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want no error", raw, err)
		}
	}

	bad := map[string]string{
		"http://127.0.0.1:8080": "internal",
		"http://10.1.2.3/":      "internal",
		"http://localhost/":     "not allowed",
		"http://foo.internal/":  "not allowed",
		"file:///etc/passwd":    "scheme",
		"ftp://example.com":     "scheme",
		"https://":              "no host",
	}
	for raw, want := range bad {
		err := ValidateURL(raw)
		if err == nil {
			t.Errorf("ValidateURL(%q) should have failed", raw)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateURL(%q) = %q, want it to contain %q", raw, err, want)
		}
	}
}
