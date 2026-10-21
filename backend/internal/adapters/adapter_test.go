package adapters

import (
	"net/http"
	"testing"
	"time"
)

func TestWorst(t *testing.T) {
	cases := []struct {
		name string
		in   []Health
		want Health
	}{
		{"no input", nil, HealthUnknown},
		{"single ok", []Health{HealthOK}, HealthOK},
		{"problem wins over ok", []Health{HealthOK, HealthDegraded, HealthPartial}, HealthPartial},
		{"major wins", []Health{HealthOK, HealthMajor, HealthDegraded}, HealthMajor},
		{"maintenance wins over ok", []Health{HealthOK, HealthMaintenance}, HealthMaintenance},
		{"unknown wins over ok", []Health{HealthOK, HealthUnknown}, HealthUnknown},
		{"unknown wins over maintenance", []Health{HealthMaintenance, HealthUnknown}, HealthUnknown},
		{"degraded wins over unknown", []Health{HealthUnknown, HealthDegraded}, HealthDegraded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Worst(tc.in...); got != tc.want {
				t.Errorf("Worst(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHealthHealthy(t *testing.T) {
	if !HealthOK.Healthy() || !HealthMaintenance.Healthy() {
		t.Error("ok and maintenance should be healthy")
	}
	for _, h := range []Health{HealthDegraded, HealthPartial, HealthMajor, HealthUnknown} {
		if h.Healthy() {
			t.Errorf("%q must not be healthy", h)
		}
	}
}

func TestRetryAfterReadsTheHeader(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"seconds", "120", 2 * time.Minute},
		{"one second", "1", time.Second},
		{"missing", "", 0},
		{"zero", "0", 0},
		{"negative", "-30", 0},
		{"junk", "soon", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if tc.value != "" {
				resp.Header.Set("Retry-After", tc.value)
			}
			if got := RetryAfter(resp); got != tc.want {
				t.Errorf("RetryAfter(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestRetryAfterReadsADate(t *testing.T) {
	at := time.Now().Add(90 * time.Second).UTC().Truncate(time.Second)
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", at.Format(http.TimeFormat))

	got := RetryAfter(resp)
	if got < 80*time.Second || got > 90*time.Second {
		t.Errorf("RetryAfter(date) = %v, want about 90s", got)
	}
}

func TestRetryAfterIgnoresADateInThePast(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))

	if got := RetryAfter(resp); got != 0 {
		t.Errorf("RetryAfter(past date) = %v, want 0", got)
	}
}

func TestRetryAfterWithoutAResponse(t *testing.T) {
	if got := RetryAfter(nil); got != 0 {
		t.Errorf("RetryAfter(nil) = %v, want 0", got)
	}
}
