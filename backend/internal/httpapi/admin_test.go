package httpapi

import (
	"strings"
	"testing"

	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/store"
)

func testHandlers() *handlers {
	return &handlers{catalog: catalog.Catalog{Profiles: catalog.Profiles{
		"statuspage-v2": {
			Kind:                  "statuspage",
			Endpoint:              "/api/v2/summary.json",
			PollIntervalSeconds:   300,
			ActiveIntervalSeconds: 60,
		},
	}}}
}

func TestValidateService(t *testing.T) {
	h := testHandlers()

	cases := []struct {
		label    string
		name     string
		url      string
		profile  string
		category string
		logo     string
		aliases  []string
		status   string
		want     string
	}{
		{"published and complete", "Some Service", "https://status.example.com", "statuspage-v2", "tools", "https://example.com/logo.png", []string{"some service down"}, "published", ""},
		{"published without url", "Some Service", "", "statuspage-v2", "", "", nil, "published", "needs a status page url"},
		{"published without profile", "Some Service", "https://status.example.com", "", "", "", nil, "published", "needs an adapter profile"},
		{"parked without a profile", "Some Service", "https://status.example.com", "", "", "", nil, "no_match", ""},
		{"parked without a url", "Some Service", "", "", "", "", nil, "no_match", "needs a status page url"},
		{"proposal may be incomplete", "Some Service", "", "", "", "", nil, "proposed", ""},
		{"rejected may be incomplete", "Some Service", "", "", "", "", nil, "rejected", ""},
		{"name too short", "A", "https://status.example.com", "statuspage-v2", "", "", nil, "published", "between 2 and 80"},
		{"name too long", strings.Repeat("x", 81), "https://status.example.com", "statuspage-v2", "", "", nil, "published", "between 2 and 80"},
		{"unknown status", "Some Service", "https://status.example.com", "statuspage-v2", "", "", nil, "draft", "status must be"},
		{"unknown profile", "Some Service", "https://status.example.com", "nope", "", "", nil, "published", "unknown profile"},
		{"internal url", "Some Service", "http://127.0.0.1:9000", "statuspage-v2", "", "", nil, "published", "not allowed"},
		{"url without scheme", "Some Service", "status.example.com", "statuspage-v2", "", "", nil, "published", "scheme"},
		{"category too long", "Some Service", "https://status.example.com", "statuspage-v2", strings.Repeat("c", 41), "", nil, "published", "category"},
		{"logo is not a url", "Some Service", "https://status.example.com", "statuspage-v2", "", "not a url", nil, "published", "logo url"},
		{"too many aliases", "Some Service", "https://status.example.com", "statuspage-v2", "", "", manyAliases(21), "published", "at most 20"},
		{"alias too short", "Some Service", "https://status.example.com", "statuspage-v2", "", "", []string{"x"}, "published", "each alias"},
		{"alias too long", "Some Service", "https://status.example.com", "statuspage-v2", "", "", []string{strings.Repeat("a", 61)}, "published", "each alias"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := h.validateService(tc.name, tc.url, tc.profile, tc.category, tc.logo, tc.aliases, tc.status)
			if tc.want == "" {
				if got != "" {
					t.Errorf("validateService = %q, want no problem", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("validateService = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

func TestCleanAliases(t *testing.T) {
	in := []string{"  spaced  ", "", "   ", "line\nbreak", "ok"}
	got := cleanAliases(&in)

	want := []string{"spaced", "line break", "ok"}
	if len(got) != len(want) {
		t.Fatalf("cleanAliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alias %d = %q, want %q", i, got[i], want[i])
		}
	}

	empty := cleanAliases(nil)
	if empty == nil {
		t.Error("cleanAliases(nil) should return an empty slice, not nil")
	}
	if len(empty) != 0 {
		t.Errorf("cleanAliases(nil) = %v, want empty", empty)
	}
}

func TestStringValue(t *testing.T) {
	value := "set"
	if got := stringValue(&value); got != "set" {
		t.Errorf("stringValue = %q, want set", got)
	}
	if got := stringValue(nil); got != "" {
		t.Errorf("stringValue(nil) = %q, want empty", got)
	}
}

func TestCanSeePollLog(t *testing.T) {
	owner := int64(7)

	cases := []struct {
		label   string
		viewer  store.Viewer
		ownerID *int64
		want    bool
	}{
		{"staff on a public vendor", store.Viewer{UserID: 1, IsStaff: true}, nil, true},
		{"staff on somebody else's service", store.Viewer{UserID: 1, IsStaff: true}, &owner, true},
		{"the owner", store.Viewer{UserID: owner}, &owner, true},
		{"another signed in visitor", store.Viewer{UserID: 2}, &owner, false},
		{"another signed in visitor on a public vendor", store.Viewer{UserID: 2}, nil, false},
		{"the public", store.Public(), nil, false},
		{"the public on somebody else's service", store.Public(), &owner, false},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if got := canSeePollLog(tc.viewer, tc.ownerID); got != tc.want {
				t.Errorf("canSeePollLog = %v, want %v", got, tc.want)
			}
		})
	}
}

func manyAliases(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "alias number"
	}
	return out
}
