package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/slack"
	"downradar.info/backend/internal/store"
)

// SlackApp is the part of the site's Slack app these handlers use, so a test can
// stand in for Slack instead of reaching it.
type SlackApp interface {
	AuthorizeURL(state, redirectURI string) string
	Exchange(ctx context.Context, code, redirectURI string) (slack.Webhook, error)
}

// The cookie that carries an install from the alert page to the callback. Slack
// sends the browser back to one fixed address, which is what the app registers,
// so the alert the install belongs to has to travel beside the state rather than
// in the address. The state itself is what proves the answer belongs to a flow
// this browser started.
const (
	slackStateCookie = "dr_slack"
	slackStateTTL    = 10 * time.Minute
	slackCookiePath  = "/api/v1/"
)

type slackFlow struct {
	UserID     int64    `json:"user"`
	ServiceID  int64    `json:"service"`
	VendorSlug string   `json:"slug"`
	VendorName string   `json:"name"`
	Whole      bool     `json:"whole"`
	Components []string `json:"components,omitempty"`
	Expires    int64    `json:"exp"`
}

// slackRedirectURL is the address registered with the Slack app. It is the
// public site rather than the API's own address, because the person's browser
// follows it and the cookies have to be set on the site's domain.
func (h *handlers) slackRedirectURL() string {
	return strings.TrimSuffix(h.publicURL, "/") + "/api/v1/slack/callback"
}

// slackConnect sends the browser to Slack. The page has already decided what
// the alert watches, and what it picked is carried in the cookie with the alert
// it belongs to, so the answer can be saved without asking again.
func (h *handlers) slackConnect(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	serviceID, name, err := h.store.PublicService(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/alerts?slack=failed", http.StatusFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	if h.slack == nil || !h.alertsEnabled {
		h.slackBack(w, r, slug, "unavailable")
		return
	}

	user := h.user(r)
	watching, err := h.store.UserWatchesService(r.Context(), user.ID, serviceID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !watching {
		watched, err := h.store.WatchedServiceCount(r.Context(), user.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		limit, err := h.userAlertLimit(r, user.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if watched+1 > limit {
			h.slackBack(w, r, slug, "limit")
			return
		}
	}

	whole, components, reason := slackTargets(r)
	if reason != "" {
		h.slackBack(w, r, slug, reason)
		return
	}

	state, err := auth.NewToken()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.setSlackState(w, state, slackFlow{
		UserID:     user.ID,
		ServiceID:  serviceID,
		VendorSlug: slug,
		VendorName: name,
		Whole:      whole,
		Components: components,
		Expires:    time.Now().Add(slackStateTTL).Unix(),
	})

	h.log.Info("slack install started", "vendor", name, "user", user.Email,
		"whole", whole, "components", len(components))
	http.Redirect(w, r, h.slack.AuthorizeURL(state, h.slackRedirectURL()), http.StatusFound)
}

// slackTargets reads what the page had ticked. The list is kept in the cookie,
// which is a few kilobytes, so a vendor with an unusual number of components is
// refused rather than cut short: saving the alert first is the way to connect
// one of those.
func slackTargets(r *http.Request) (bool, []string, string) {
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

// slackCallback takes the answer back. Everything that can go wrong ends on the
// alert page with a reason in the address, because the visitor arrived by
// following a link and a JSON answer would be a dead end.
func (h *handlers) slackCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	state, flow, ok := h.takeSlackState(w, r)
	if !ok || time.Now().Unix() > flow.Expires ||
		subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 {
		h.log.Warn("slack callback did not come from this browser", "ip", h.clientIP(r))
		http.Redirect(w, r, "/alerts?slack=failed", http.StatusFound)
		return
	}

	if h.slack == nil || !h.alertsEnabled {
		h.slackBack(w, r, flow.VendorSlug, "unavailable")
		return
	}

	query := r.URL.Query()
	if refusal := query.Get("error"); refusal != "" {
		h.log.Info("slack install refused", "vendor", flow.VendorSlug, "error", refusal)
		h.slackBack(w, r, flow.VendorSlug, "refused")
		return
	}

	code := query.Get("code")
	if code == "" {
		h.slackBack(w, r, flow.VendorSlug, "failed")
		return
	}

	hook, err := h.slack.Exchange(r.Context(), code, h.slackRedirectURL())
	if err != nil {
		h.log.Warn("slack install exchange failed", "vendor", flow.VendorSlug, "error", err)
		h.slackBack(w, r, flow.VendorSlug, "failed")
		return
	}

	webhookURL, problem := parseSlack(hook.URL)
	if problem != "" || webhookURL == "" {
		h.log.Warn("slack install answer carried no usable webhook",
			"vendor", flow.VendorSlug, "error", problem)
		h.slackBack(w, r, flow.VendorSlug, "failed")
		return
	}

	componentIDs, err := h.store.ComponentIDs(r.Context(), flow.ServiceID, flow.Components)
	if errors.Is(err, store.ErrNotFound) {
		h.log.Warn("slack install carried components that are gone", "vendor", flow.VendorSlug)
		h.slackBack(w, r, flow.VendorSlug, "failed")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	token, err := auth.NewToken()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	err = h.store.SubscribeAlert(r.Context(), flow.ServiceID, &flow.UserID, store.AlertChange{
		Slack: &store.AlertSlack{URL: webhookURL, Channel: hook.Channel},
		Targets: store.AlertTargets{
			Whole:      flow.Whole,
			Components: componentIDs,
		},
		SlackToken: token,
	})
	if err != nil {
		h.log.Error("saving the slack install failed", "vendor", flow.VendorSlug, "error", err)
		h.slackBack(w, r, flow.VendorSlug, "failed")
		return
	}

	// The channel is saved, but the person would otherwise have to wait for a
	// change to see that it works. A refusal here does not undo the install: the
	// next change will try again, and the page still says it is connected.
	name := flow.VendorName
	if name == "" {
		name = flow.VendorSlug
	}
	if h.notifier != nil {
		if err := h.notifier.WelcomeSlack(r.Context(), webhookURL, name, flow.VendorSlug); err != nil {
			h.log.Warn("slack welcome message failed", "vendor", flow.VendorSlug, "error", err)
		}
	}

	h.log.Info("slack connected", "vendor", flow.VendorSlug, "user", flow.UserID,
		"team", hook.Team, "channel", hook.Channel,
		"whole", flow.Whole, "components", len(componentIDs))
	h.slackBack(w, r, flow.VendorSlug, "connected")
}

// slackBack is the one place the alert page is told how an install ended.
func (h *handlers) slackBack(w http.ResponseWriter, r *http.Request, slug, reason string) {
	query := url.Values{"slack": {reason}}
	http.Redirect(w, r, "/alerts/"+url.PathEscape(slug)+"?"+query.Encode(), http.StatusFound)
}

// setSlackState puts the state and the alert it belongs to in a cookie. The
// cookie is short lived and spent by the first callback, whatever the answer is.
func (h *handlers) setSlackState(w http.ResponseWriter, state string, flow slackFlow) {
	raw, _ := json.Marshal(flow)
	value := state + ":" + base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name:     slackStateCookie,
		Value:    value,
		Path:     slackCookiePath,
		MaxAge:   int(slackStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.sessionSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *handlers) takeSlackState(w http.ResponseWriter, r *http.Request) (state string, flow slackFlow, ok bool) {
	cookie, err := r.Cookie(slackStateCookie)
	h.clearSlackState(w)
	if err != nil {
		return "", slackFlow{}, false
	}

	raw, encoded, found := strings.Cut(cookie.Value, ":")
	if !found {
		return "", slackFlow{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", slackFlow{}, false
	}
	if err := json.Unmarshal(decoded, &flow); err != nil {
		return "", slackFlow{}, false
	}
	if flow.Expires == 0 || flow.UserID == 0 || flow.ServiceID == 0 || flow.VendorSlug == "" {
		return "", slackFlow{}, false
	}
	return raw, flow, true
}

func (h *handlers) clearSlackState(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     slackStateCookie,
		Value:    "",
		Path:     slackCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.sessionSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
