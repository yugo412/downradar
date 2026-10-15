package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func countingHandler(called *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*called++
		w.WriteHeader(http.StatusOK)
	}
}

func TestIPLimiterAllowsBurstThenBlocks(t *testing.T) {
	limiter := newIPLimiter(time.Second, 2)

	if !limiter.allow("10.0.0.1") || !limiter.allow("10.0.0.1") {
		t.Fatal("the first two requests should pass within the burst")
	}
	if limiter.allow("10.0.0.1") {
		t.Error("the third request should be blocked")
	}
	if !limiter.allow("10.0.0.2") {
		t.Error("a different IP must have its own budget")
	}
}

func TestIPLimiterDisabled(t *testing.T) {
	limiter := newIPLimiter(0, 0)
	for i := 0; i < 100; i++ {
		if !limiter.allow("10.0.0.1") {
			t.Fatal("a disabled limiter must allow everything")
		}
	}
}

func TestReadEndpointLimitsByIP(t *testing.T) {
	h := &handlers{log: quietLogger(), readLimiter: newIPLimiter(time.Second, 2)}

	called := 0
	handler := h.publicRead(countingHandler(&called))

	codes := make([]int, 0, 4)
	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/vendors", nil))
		codes = append(codes, rec.Code)
	}

	if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
		t.Errorf("first two should pass, got %v", codes)
	}
	if codes[2] != http.StatusTooManyRequests || codes[3] != http.StatusTooManyRequests {
		t.Errorf("later requests should be limited, got %v", codes)
	}
	if called != 2 {
		t.Errorf("handler called %d times, want 2", called)
	}
}

func TestLimitedResponseHasRetryAfterAndCORS(t *testing.T) {
	h := &handlers{log: quietLogger(), readLimiter: newIPLimiter(10*time.Minute, 1)}
	handler := h.publicRead(func(w http.ResponseWriter, r *http.Request) {})

	handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/vendors", nil))

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/vendors", nil))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "600" {
		t.Errorf("Retry-After = %q, want 600 (the limiter interval, not a fixed 60)", got)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("CORS header must still be present on a limited response")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	cases := []struct {
		interval time.Duration
		want     int
	}{
		{time.Minute / 120, 1},
		{time.Second, 1},
		{10 * time.Minute, 600},
		{time.Hour, 3600},
	}
	for _, tc := range cases {
		limiter := newIPLimiter(tc.interval, 1)
		if got := limiter.retryAfterSeconds(); got != tc.want {
			t.Errorf("interval %v: got %d, want %d", tc.interval, got, tc.want)
		}
	}

	var disabled *ipLimiter
	if got := disabled.retryAfterSeconds(); got != 1 {
		t.Errorf("disabled limiter: got %d, want 1", got)
	}
}

func TestRateLimitMessagesDifferByEndpoint(t *testing.T) {
	h := &handlers{
		log:            quietLogger(),
		readLimiter:    newIPLimiter(time.Hour, 1),
		suggestLimiter: newIPLimiter(time.Hour, 1),
	}

	readHandler := h.publicRead(func(w http.ResponseWriter, r *http.Request) {})
	readHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/vendors", nil))
	readRec := httptest.NewRecorder()
	readHandler(readRec, httptest.NewRequest(http.MethodGet, "/api/v1/vendors", nil))

	writeHandler := h.publicWrite(func(w http.ResponseWriter, r *http.Request) {})
	writeHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/suggest", nil))
	writeRec := httptest.NewRecorder()
	writeHandler(writeRec, httptest.NewRequest(http.MethodPost, "/api/v1/suggest", nil))

	readBody := readRec.Body.String()
	writeBody := writeRec.Body.String()
	if readBody == writeBody {
		t.Errorf("both endpoints returned the same message: %q", readBody)
	}
	if !strings.Contains(writeBody, "suggestion") {
		t.Errorf("the write message should mention suggestions, got %q", writeBody)
	}
}

func TestReadEndpointBypassesLimitForInternalToken(t *testing.T) {
	h := &handlers{
		log:           quietLogger(),
		readLimiter:   newIPLimiter(time.Second, 1),
		internalToken: "s3cret",
	}

	called := 0
	handler := h.publicRead(countingHandler(&called))

	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/vendors", nil)
		req.Header.Set("X-Internal-Token", "s3cret")
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("internal request %d got %d, want 200", i, rec.Code)
		}
	}
	if called != 20 {
		t.Errorf("handler called %d times, want 20", called)
	}
}

func TestWriteEndpointDoesNotBypassForInternalToken(t *testing.T) {
	h := &handlers{
		log:            quietLogger(),
		suggestLimiter: newIPLimiter(time.Hour, 1),
		internalToken:  "s3cret",
	}

	called := 0
	handler := h.publicWrite(countingHandler(&called))

	pass, limited := 0, 0
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/suggest", nil)
		req.Header.Set("X-Internal-Token", "s3cret")
		rec := httptest.NewRecorder()
		handler(rec, req)
		switch rec.Code {
		case http.StatusOK:
			pass++
		case http.StatusTooManyRequests:
			limited++
		}
	}

	if pass != 1 || limited != 4 {
		t.Errorf("pass = %d, limited = %d, want 1 and 4", pass, limited)
	}
}

func TestWriteEndpointUsesForwardedClientIP(t *testing.T) {
	h := &handlers{
		log:            quietLogger(),
		suggestLimiter: newIPLimiter(time.Hour, 1),
		internalToken:  "s3cret",
	}

	called := 0
	handler := h.publicWrite(countingHandler(&called))

	for _, forwarded := range []string{"203.0.113.7", "203.0.113.8", "203.0.113.9"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/suggest", nil)
		req.Header.Set("X-Internal-Token", "s3cret")
		req.Header.Set("X-Forwarded-For", forwarded)
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("client %s got %d, want 200 (each IP has its own budget)", forwarded, rec.Code)
		}
	}
}

func TestForwardedHeaderIgnoredWithoutInternalToken(t *testing.T) {
	h := &handlers{
		log:            quietLogger(),
		suggestLimiter: newIPLimiter(time.Hour, 1),
		internalToken:  "s3cret",
	}

	called := 0
	handler := h.publicWrite(countingHandler(&called))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/suggest", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113."+string(rune('0'+i)))
		rec := httptest.NewRecorder()
		handler(rec, req)
	}

	if called != 1 {
		t.Errorf("handler called %d times, want 1: spoofed X-Forwarded-For must be ignored", called)
	}
}

func TestPreflightIsNotCounted(t *testing.T) {
	h := &handlers{log: quietLogger(), readLimiter: newIPLimiter(time.Hour, 1)}

	called := 0
	handler := h.publicRead(countingHandler(&called))

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodOptions, "/api/v1/vendors", nil))

		if rec.Code != http.StatusNoContent {
			t.Fatalf("preflight got %d, want 204", rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" {
			t.Errorf("Allow-Methods = %q", rec.Header().Get("Access-Control-Allow-Methods"))
		}
	}

	if called != 0 {
		t.Errorf("preflight must not reach the handler, got %d calls", called)
	}

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/vendors", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("the first real request should still pass, got %d", rec.Code)
	}
}

func TestReadSetsCacheControl(t *testing.T) {
	h := &handlers{log: quietLogger(), readLimiter: newIPLimiter(time.Hour, 1)}
	handler := h.publicRead(func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/vendors", nil))

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60, stale-while-revalidate=300" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestWriteSetsNoCacheControl(t *testing.T) {
	h := &handlers{log: quietLogger(), suggestLimiter: newIPLimiter(time.Hour, 1)}
	handler := h.publicWrite(func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/api/v1/suggest", nil))

	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("a POST response must not be cached, got %q", got)
	}
}

func TestRemoteIP(t *testing.T) {
	cases := map[string]string{
		"10.0.0.1:54321": "10.0.0.1",
		"[::1]:8080":     "::1",
		"no-port":        "no-port",
	}
	for in, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = in
		if got := remoteIP(req); got != want {
			t.Errorf("remoteIP(%q) = %q, want %q", in, got, want)
		}
	}
}
