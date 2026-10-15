package httpapi

import (
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ipLimiter struct {
	mu       sync.Mutex
	entries  map[string]*limiterEntry
	limit    rate.Limit
	burst    int
	interval time.Duration
	ttl      time.Duration
	sweptAt  time.Time
}

type limiterEntry struct {
	limiter *rate.Limiter
	seen    time.Time
}

func newIPLimiter(interval time.Duration, burst int) *ipLimiter {
	if interval <= 0 {
		return nil
	}
	return &ipLimiter{
		entries:  make(map[string]*limiterEntry),
		limit:    rate.Limit(1 / interval.Seconds()),
		burst:    burst,
		interval: interval,
		ttl:      10 * time.Minute,
		sweptAt:  time.Now(),
	}
}

func (l *ipLimiter) retryAfterSeconds() int {
	if l == nil || l.interval <= 0 {
		return 1
	}
	seconds := int(math.Ceil(l.interval.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (l *ipLimiter) allow(ip string) bool {
	if l == nil {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if now.Sub(l.sweptAt) > time.Minute {
		for key, entry := range l.entries {
			if now.Sub(entry.seen) > l.ttl {
				delete(l.entries, key)
			}
		}
		l.sweptAt = now
	}

	entry, ok := l.entries[ip]
	if !ok {
		entry = &limiterEntry{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.entries[ip] = entry
	}
	entry.seen = now
	return entry.limiter.Allow()
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *handlers) clientIP(r *http.Request) string {
	if h.isInternal(r) {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			first, _, _ := strings.Cut(forwarded, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
	}
	return remoteIP(r)
}
