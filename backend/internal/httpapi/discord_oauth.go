package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/discord"
	"downradar.info/backend/internal/store"
)

// DiscordApp is the part of the site's Discord app these handlers use, so a test
// can stand in for Discord instead of reaching it.
type DiscordApp interface {
	AuthorizeURL(state, redirectURI string) string
	Exchange(ctx context.Context, code, redirectURI string) (discord.Webhook, error)
}

// The cookie carrying an install from the alert page to the callback. Discord
// sends the browser back to one fixed address, so the alert travels beside the
// state rather than in the address; the state proves this browser started it.
const discordStateCookie = "dr_discord"

// discordRedirectURL is the address registered with the Discord application. It
// is the public site rather than the API's own address, because the person's
// browser follows it and the cookies have to be set on the site's domain.
func (h *handlers) discordRedirectURL() string {
	return strings.TrimSuffix(h.publicURL, "/") + "/api/v1/discord/callback"
}

// discordConnect sends the browser to Discord. The page has already decided what
// the alert watches, and what it picked is carried in the cookie with the alert
// it belongs to, so the answer can be saved without asking again.
func (h *handlers) discordConnect(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	serviceID, name, err := h.store.PublicService(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/alerts?discord=failed", http.StatusFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	if h.discord == nil || !h.alertsEnabled {
		h.discordBack(w, r, slug, "unavailable")
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
			h.discordBack(w, r, slug, "limit")
			return
		}
	}

	whole, components, reason := alertConnectTargets(r)
	if reason != "" {
		h.discordBack(w, r, slug, reason)
		return
	}

	state, err := auth.NewToken()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.setAlertInstallState(w, discordStateCookie, state, alertInstallFlow{
		UserID:     user.ID,
		ServiceID:  serviceID,
		VendorSlug: slug,
		VendorName: name,
		Whole:      whole,
		Components: components,
		Expires:    time.Now().Add(alertInstallStateTTL).Unix(),
	})

	h.log.Info("discord install started", "vendor", name, "user", user.Email,
		"whole", whole, "components", len(components))
	http.Redirect(w, r, h.discord.AuthorizeURL(state, h.discordRedirectURL()), http.StatusFound)
}

// discordCallback takes the answer back. Everything that can go wrong ends on
// the alert page with a reason in the address, because the visitor arrived by
// following a link and a JSON answer would be a dead end.
func (h *handlers) discordCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	state, flow, ok := h.takeAlertInstallState(w, r, discordStateCookie)
	if !ok || time.Now().Unix() > flow.Expires ||
		subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 {
		h.log.Warn("discord callback did not come from this browser", "ip", h.clientIP(r))
		http.Redirect(w, r, "/alerts?discord=failed", http.StatusFound)
		return
	}

	if h.discord == nil || !h.alertsEnabled {
		h.discordBack(w, r, flow.VendorSlug, "unavailable")
		return
	}

	query := r.URL.Query()
	if refusal := query.Get("error"); refusal != "" {
		h.log.Info("discord install refused", "vendor", flow.VendorSlug, "error", refusal)
		h.discordBack(w, r, flow.VendorSlug, "refused")
		return
	}

	code := query.Get("code")
	if code == "" {
		h.discordBack(w, r, flow.VendorSlug, "failed")
		return
	}

	hook, err := h.discord.Exchange(r.Context(), code, h.discordRedirectURL())
	if err != nil {
		h.log.Warn("discord install exchange failed", "vendor", flow.VendorSlug, "error", err)
		h.discordBack(w, r, flow.VendorSlug, "failed")
		return
	}

	webhookURL, problem := parseDiscord(hook.URL)
	if problem != "" || webhookURL == "" {
		h.log.Warn("discord install answer carried no usable webhook",
			"vendor", flow.VendorSlug, "error", problem)
		h.discordBack(w, r, flow.VendorSlug, "failed")
		return
	}

	componentIDs, err := h.store.ComponentIDs(r.Context(), flow.ServiceID, flow.Components)
	if errors.Is(err, store.ErrNotFound) {
		h.log.Warn("discord install carried components that are gone", "vendor", flow.VendorSlug)
		h.discordBack(w, r, flow.VendorSlug, "failed")
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
		Discord: &store.AlertDiscord{URL: webhookURL, Channel: hook.Channel},
		Targets: store.AlertTargets{
			Whole:      flow.Whole,
			Components: componentIDs,
		},
		DiscordToken: token,
	})
	if err != nil {
		h.log.Error("saving the discord install failed", "vendor", flow.VendorSlug, "error", err)
		h.discordBack(w, r, flow.VendorSlug, "failed")
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
		if err := h.notifier.WelcomeDiscord(r.Context(), webhookURL, name, flow.VendorSlug); err != nil {
			h.log.Warn("discord welcome message failed", "vendor", flow.VendorSlug, "error", err)
		}
	}

	h.log.Info("discord connected", "vendor", flow.VendorSlug, "user", flow.UserID,
		"guild", hook.Guild, "channel", hook.Channel,
		"whole", flow.Whole, "components", len(componentIDs))
	h.discordBack(w, r, flow.VendorSlug, "connected")
}

// discordBack is the one place the alert page is told how an install ended.
func (h *handlers) discordBack(w http.ResponseWriter, r *http.Request, slug, reason string) {
	query := url.Values{"discord": {reason}}
	http.Redirect(w, r, "/alerts/"+url.PathEscape(slug)+"?"+query.Encode(), http.StatusFound)
}
