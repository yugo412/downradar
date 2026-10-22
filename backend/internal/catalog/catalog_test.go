package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// servicesFilePath prefers SERVICES_FILE and falls back to walking up from the
// working directory, since the host and container keep the file in different places.
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
	cat, err := Load(servicesFilePath(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Profiles) < 4 {
		t.Fatalf("only %d profiles parsed, file looks broken", len(cat.Profiles))
	}

	for name, p := range cat.Profiles {
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

	// The detect queue decides what is tried when a vendor arrives without a
	// profile, so every entry has to be a profile that exists.
	if len(cat.Detect) == 0 {
		t.Error("services.yaml declares no detect queue, nothing would be detected")
	}
	for _, name := range cat.Detect {
		if _, ok := cat.Profiles[name]; !ok {
			t.Errorf("detect queue names %q, which is not a declared profile", name)
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
		{
			name: "detect names a profile that is not declared",
			body: `
detect: [nope]
profiles:
  a: {kind: statuspage, poll_interval_seconds: 300, active_interval_seconds: 60}
`,
			want: "not declared",
		},
		{
			name: "detect names the same profile twice",
			body: `
detect: [a, a]
profiles:
  a: {kind: statuspage, poll_interval_seconds: 300, active_interval_seconds: 60}
`,
			want: "twice",
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

func TestLoadReadsDescriptionsAndTheDetectQueue(t *testing.T) {
	cat, err := Load(writeTemp(t, `
detect:
  - second
  - first
profiles:
  first: {kind: statuspage, description: The first one, poll_interval_seconds: 300, active_interval_seconds: 60}
  second: {kind: statuspage, description: The second one, poll_interval_seconds: 300, active_interval_seconds: 60}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := cat.Profiles["first"].Description; got != "The first one" {
		t.Errorf("description = %q", got)
	}
	if got := cat.Profiles["second"].Description; got != "The second one" {
		t.Errorf("description = %q", got)
	}

	// The queue keeps the order it was written in, not the map order.
	want := []string{"second", "first"}
	if len(cat.Detect) != len(want) {
		t.Fatalf("detect = %v, want %v", cat.Detect, want)
	}
	for i := range want {
		if cat.Detect[i] != want[i] {
			t.Fatalf("detect = %v, want %v", cat.Detect, want)
		}
	}

	candidates := cat.Candidates()
	if len(candidates) != 2 || candidates[0].Name != "second" {
		t.Errorf("candidates = %v, want them in queue order", candidates)
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
		"file:///etc/passwd":    "Scheme",
		"ftp://example.com":     "Scheme",
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

// The picker offers only the profiles a vendor can generally be put on, so a
// profile written for one vendor's own page has to be able to say so.
func TestLoadReadsVisibility(t *testing.T) {
	cat, err := Load(writeTemp(t, `
profiles:
  shown: {kind: statuspage, poll_interval_seconds: 300, active_interval_seconds: 60}
  hidden: {kind: statuspage, poll_interval_seconds: 300, active_interval_seconds: 60, visible: false}
  again: {kind: statuspage, poll_interval_seconds: 300, active_interval_seconds: 60, visible: true}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cat.Profiles["shown"].Visible {
		t.Error("a profile without the field should be offered")
	}
	if !cat.Profiles["again"].Visible {
		t.Error("visible: true should be offered")
	}
	if cat.Profiles["hidden"].Visible {
		t.Error("visible: false should keep the profile out of the picker")
	}
}
