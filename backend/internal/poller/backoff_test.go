package poller

import (
	"net/http"
	"testing"
	"time"
)

const neutralJitter = 0.5

func TestRefusalWaitHonoursRetryAfter(t *testing.T) {
	wait, refusal := refusalWait(http.StatusTooManyRequests, 1, 5*time.Minute, 90*time.Second, neutralJitter)
	if !refusal {
		t.Fatal("429 should be a refusal")
	}
	if wait != 90*time.Second {
		t.Errorf("wait = %v, want the 90s the page asked for", wait)
	}
}

func TestRefusalWaitCapsAnOutrageousRetryAfter(t *testing.T) {
	wait, _ := refusalWait(http.StatusServiceUnavailable, 1, 5*time.Minute, 48*time.Hour, neutralJitter)
	if wait != retryAfterCap {
		t.Errorf("wait = %v, want it capped at %v", wait, retryAfterCap)
	}
}

func TestRefusalWaitDoublesWithEachRefusal(t *testing.T) {
	cases := map[int]time.Duration{
		1: 5 * time.Minute,
		2: 10 * time.Minute,
		3: 20 * time.Minute,
	}
	for refusals, want := range cases {
		wait, _ := refusalWait(http.StatusTooManyRequests, refusals, 5*time.Minute, 0, neutralJitter)
		if wait != want {
			t.Errorf("refusals = %d, wait = %v, want %v", refusals, wait, want)
		}
	}
}

func TestRefusalWaitStopsAtTheCap(t *testing.T) {
	wait, _ := refusalWait(http.StatusServiceUnavailable, 20, 5*time.Minute, 0, neutralJitter)
	if wait != refusalCap {
		t.Errorf("wait = %v, want it capped at %v", wait, refusalCap)
	}
}

func TestForbiddenGoesOnTheSlowSchedule(t *testing.T) {
	wait, refusal := refusalWait(http.StatusForbidden, 1, 5*time.Minute, 0, neutralJitter)
	if !refusal {
		t.Fatal("403 should be a refusal")
	}
	if wait != blockedWait {
		t.Errorf("wait = %v, want the slow schedule of %v", wait, blockedWait)
	}

	// It does not grow with the refusal count: the schedule stays slow and flat.
	again, _ := refusalWait(http.StatusForbidden, 9, 5*time.Minute, 0, neutralJitter)
	if again != wait {
		t.Errorf("wait after nine refusals = %v, want %v", again, wait)
	}
}

func TestOtherFailuresAreNotRefusals(t *testing.T) {
	for _, status := range []int{0, http.StatusInternalServerError, http.StatusNotFound, http.StatusOK} {
		wait, refusal := refusalWait(status, 1, 5*time.Minute, 0, neutralJitter)
		if refusal || wait != 0 {
			t.Errorf("status %d: wait = %v, refusal = %v, want neither", status, wait, refusal)
		}
	}
}

func TestRefusalWaitIsJittered(t *testing.T) {
	low, _ := refusalWait(http.StatusServiceUnavailable, 1, 5*time.Minute, 0, 0)
	high, _ := refusalWait(http.StatusServiceUnavailable, 1, 5*time.Minute, 0, 1)

	if low >= 5*time.Minute || high <= 5*time.Minute {
		t.Errorf("wait = %v..%v, want it spread either side of 5m", low, high)
	}
	if low < 4*time.Minute || high > 6*time.Minute {
		t.Errorf("wait = %v..%v, want the spread kept near 10%%", low, high)
	}
}

func TestPollIntervalHasAFloor(t *testing.T) {
	for _, interval := range []time.Duration{time.Second, 10 * time.Second, time.Minute} {
		got := pollInterval("floor-test", interval)
		if got < pollFloor {
			t.Errorf("interval %v: got %v, want at least %v", interval, got, pollFloor)
		}
	}
}

func TestPollIntervalSpreadsVendorsThatShareAnInterval(t *testing.T) {
	const interval = 5 * time.Minute

	a := pollInterval("alpha", interval)
	b := pollInterval("beta", interval)

	if a == b {
		t.Errorf("two vendors got the same interval %v, want them spread apart", a)
	}
	if a != pollInterval("alpha", interval) {
		t.Error("the spread should be stable for a vendor, not drawn again each cycle")
	}
	for _, got := range []time.Duration{a, b} {
		if got < interval || got >= interval+interval/10 {
			t.Errorf("interval = %v, want it between %v and %v", got, interval, interval+interval/10)
		}
	}
}
