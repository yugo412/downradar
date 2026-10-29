package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// The one click install flows for Slack and Discord share the same shape: a
// short lived cookie carries the alert while the browser is away, and the state
// proves the browser that comes back started it.
const (
	alertInstallStateTTL   = 10 * time.Minute
	alertInstallCookiePath = "/api/v1/"
)

type alertInstallFlow struct {
	UserID     int64    `json:"user"`
	ServiceID  int64    `json:"service"`
	VendorSlug string   `json:"slug"`
	VendorName string   `json:"name"`
	Whole      bool     `json:"whole"`
	Components []string `json:"components,omitempty"`
	Expires    int64    `json:"exp"`
}

// alertConnectTargets reads what the page had ticked. The list lives in a cookie
// of a few kilobytes, so a vendor with an unusual number of components is
// refused rather than cut short; saving the alert first is the way to connect
// one.
func alertConnectTargets(r *http.Request) (bool, []string, string) {
	query := r.URL.Query()
	whole := query.Get("wholeService") == "true"
	components := query["components"]

	if !whole && len(components) == 0 {
		return false, nil, "pick"
	}

	total := 0
	for _, id := range components {
		if id == "" || len(id) > 200 {
			return false, nil, "failed"
		}
		total += len(id) + 1
		if total > 1800 {
			return false, nil, "too-many"
		}
	}
	return whole, components, ""
}

// setAlertInstallState puts the state and the alert it belongs to in a cookie.
// The cookie is short lived and spent by the first callback, whatever the answer
// is.
func (h *handlers) setAlertInstallState(w http.ResponseWriter, cookie, state string, flow alertInstallFlow) {
	raw, _ := json.Marshal(flow)
	value := state + ":" + base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name:     cookie,
		Value:    value,
		Path:     alertInstallCookiePath,
		MaxAge:   int(alertInstallStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.sessionSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *handlers) takeAlertInstallState(w http.ResponseWriter, r *http.Request, cookie string) (string, alertInstallFlow, bool) {
	held, err := r.Cookie(cookie)
	h.clearAlertInstallState(w, cookie)
	if err != nil {
		return "", alertInstallFlow{}, false
	}

	raw, encoded, found := strings.Cut(held.Value, ":")
	if !found {
		return "", alertInstallFlow{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", alertInstallFlow{}, false
	}
	var flow alertInstallFlow
	if err := json.Unmarshal(decoded, &flow); err != nil {
		return "", alertInstallFlow{}, false
	}
	if flow.Expires == 0 || flow.UserID == 0 || flow.ServiceID == 0 || flow.VendorSlug == "" {
		return "", alertInstallFlow{}, false
	}
	return raw, flow, true
}

func (h *handlers) clearAlertInstallState(w http.ResponseWriter, cookie string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookie,
		Value:    "",
		Path:     alertInstallCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.sessionSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
