package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testAuthHandlers(token string) *handlers {
	return &handlers{
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		internalToken: token,
	}
}

func TestOriginHost(t *testing.T) {
	h := testAuthHandlers("secret")

	cases := []struct {
		label     string
		token     string
		forwarded string
		host      string
		want      string
	}{
		{"host from the proxy", "secret", "downradar.info", "api:8080", "downradar.info"},
		{"first entry of a list", "secret", "downradar.info, other.example", "api:8080", "downradar.info"},
		{"surrounding spaces are trimmed", "secret", "  downradar.info  ", "api:8080", "downradar.info"},
		{"header ignored without a token", "", "evil.example", "api:8080", "api:8080"},
		{"header ignored with a wrong token", "nope", "evil.example", "api:8080", "api:8080"},
		{"internal but no header", "secret", "", "api:8080", "api:8080"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			r.Host = tc.host
			if tc.token != "" {
				r.Header.Set("X-Internal-Token", tc.token)
			}
			if tc.forwarded != "" {
				r.Header.Set("X-Forwarded-Host", tc.forwarded)
			}
			if got := h.originHost(r); got != tc.want {
				t.Errorf("originHost = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckOrigin(t *testing.T) {
	h := testAuthHandlers("secret")

	cases := []struct {
		label     string
		method    string
		origin    string
		forwarded string
		want      bool
	}{
		{"a read is never checked", http.MethodGet, "https://evil.example", "", true},
		{"no origin header at all", http.MethodPost, "", "", true},
		{"matching origin", http.MethodPost, "http://localhost:3000", "localhost:3000", true},
		{"host compared case insensitively", http.MethodPost, "http://LocalHost:3000", "localhost:3000", true},
		{"the scheme is not compared", http.MethodPost, "https://downradar.info", "downradar.info", true},
		{"another site", http.MethodPost, "https://evil.example", "downradar.info", false},
		{"another port", http.MethodPost, "http://localhost:9999", "localhost:3000", false},
		{"origin without a host", http.MethodPost, "not a url at all", "localhost:3000", false},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/v1/auth/login", nil)
			r.Host = "api:8080"
			r.Header.Set("X-Internal-Token", "secret")
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.forwarded != "" {
				r.Header.Set("X-Forwarded-Host", tc.forwarded)
			}

			w := httptest.NewRecorder()
			if got := h.checkOrigin(w, r); got != tc.want {
				t.Errorf("checkOrigin = %v, want %v", got, tc.want)
			}
			if !tc.want && w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
		})
	}
}

func TestValidateDisplayName(t *testing.T) {
	cases := []struct {
		label string
		name  string
		want  string
	}{
		{"a normal name", "Yugo", ""},
		{"two characters is the minimum", "Yu", ""},
		{"eighty characters is the maximum", strings.Repeat("x", 80), ""},
		{"one character", "Y", "between 2 and 80"},
		{"eighty one characters", strings.Repeat("x", 81), "between 2 and 80"},
		{"empty", "", "between 2 and 80"},
		{"counted in runes, not bytes", strings.Repeat("é", 80), ""},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := validateDisplayName(tc.name)
			if tc.want == "" {
				if got != "" {
					t.Errorf("validateDisplayName = %q, want no problem", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("validateDisplayName = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}
