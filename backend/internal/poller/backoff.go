package poller

import (
	"hash/fnv"
	"net/http"
	"time"
)

const (
	// pollFloor is the shortest gap between two polls of one vendor, whatever
	// its row or profile asks for: an interval of a few seconds turns a hundred
	// vendors into a flood.
	pollFloor = time.Minute

	// jitterShare is the part of an interval that is spread across vendors, so a
	// batch added together does not come due on the same second forever.
	jitterShare = 0.1

	// refusalCap is the longest the wait grows to when a page keeps refusing
	// without saying how long it wants to be left alone.
	refusalCap = 6 * time.Hour

	// blockedWait is the slow schedule a page that answers 403 is put on. That
	// answer says the caller looks like a datacenter rather than a browser, and
	// asking again sooner is what it is refusing.
	blockedWait = time.Hour

	// retryAfterCap is the longest wait a page can ask for through Retry-After.
	// The header is a hint from the other side, and a broken one must not take a
	// vendor out of the catalogue for a year.
	retryAfterCap = 24 * time.Hour
)

// baseInterval is how often a vendor is polled while it is healthy, and how
// often once it reports a problem.
func baseInterval(j job) time.Duration {
	if !j.row.Status.Healthy() {
		return time.Duration(j.target.ActiveIntervalSeconds) * time.Second
	}
	return time.Duration(j.target.PollIntervalSeconds) * time.Second
}

// pollInterval is the gap one vendor waits between two polls: its own interval,
// raised to the floor and shifted by a share derived from its slug. That share
// is stable, so a vendor keeps its offset instead of drifting with every cycle.
func pollInterval(slug string, interval time.Duration) time.Duration {
	if interval < pollFloor {
		interval = pollFloor
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(slug))
	share := float64(h.Sum32()%1000) / 1000 * jitterShare
	return interval + time.Duration(float64(interval)*share)
}

// refusalWait is how long a vendor that refused a poll is left alone, and
// whether the answer was a refusal at all. Retry-After wins when the page sent
// one, because it knows better than a curve here. Without it the wait starts at
// the vendor's own interval and doubles with each refusal in a row. A 403 is
// put on the slow schedule instead. jitter is a value in [0, 1), which spreads
// vendors that were refused together.
func refusalWait(status, refusals int, interval, retryAfter time.Duration, jitter float64) (time.Duration, bool) {
	switch status {
	case http.StatusForbidden:
		return spread(blockedWait, jitter), true

	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		if retryAfter > 0 {
			return min(retryAfter, retryAfterCap), true
		}
		if interval < pollFloor {
			interval = pollFloor
		}
		wait := interval
		for i := 1; i < refusals && wait < refusalCap; i++ {
			wait *= 2
		}
		return spread(min(wait, refusalCap), jitter), true
	}

	return 0, false
}

// spread moves a wait by up to jitterShare either way, so the vendors that were
// refused together do not all come back on the same second.
func spread(wait time.Duration, jitter float64) time.Duration {
	return time.Duration(float64(wait) * (1 + jitterShare*(2*jitter-1)))
}
