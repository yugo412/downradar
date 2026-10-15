package httpapi

import (
	"strings"
	"testing"
)

func TestValidateSuggestion(t *testing.T) {
	cases := []struct {
		name    string
		service string
		url     string
		want    string
	}{
		{"ok with url", "Some Service", "https://status.example.com", ""},
		{"ok without url", "Some Service", "", ""},
		{"ok with http", "Some Service", "http://status.example.com", ""},
		{"missing name", "", "https://status.example.com", "required"},
		{"name too short", "A", "", "between 2 and 80"},
		{"name too long", strings.Repeat("x", 81), "", "between 2 and 80"},
		{"name at the limit", strings.Repeat("x", 80), "", ""},
		{"url without scheme", "Some Service", "status.example.com", "http:// or https://"},
		{"url with wrong scheme", "Some Service", "ftp://example.com", "http:// or https://"},
		{"url without host", "Some Service", "https://", "http:// or https://"},
		{"url too long", "Some Service", "https://example.com/" + strings.Repeat("a", 300), "too long"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateSuggestion(tc.service, tc.url)
			if tc.want == "" {
				if got != "" {
					t.Errorf("validateSuggestion(%q, %q) = %q, want no error", tc.service, tc.url, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("validateSuggestion(%q, %q) = %q, want it to contain %q", tc.service, tc.url, got, tc.want)
			}
		})
	}
}

func TestSanitizeAndClean(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Plain Name", "Plain Name"},
		{"  padded  ", "padded"},
		{"multi   spaces", "multi spaces"},
		{"line\nbreak", "line break"},
		{"tab\there", "tab here"},
		{"null\x00byte", "nullbyte"},
		{"bell\x07", "bell"},
	}

	for _, tc := range cases {
		if got := clean(sanitize(tc.in)); got != tc.want {
			t.Errorf("clean(sanitize(%q)) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
