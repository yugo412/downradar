package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

type alertRequest struct {
	Email string `json:"email"`
}

// subscribeAlert takes an address for one vendor. No account is needed, which is
// the point: the alert is the address. When the visitor happens to be signed in
// their id is recorded with it, so the row can be tied to them later without
// asking again, and they are free to give any address they like.
func (h *handlers) subscribeAlert(w http.ResponseWriter, r *http.Request) {
	if !h.alertsEnabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Alerts are not available right now",
		})
		return
	}

	var body alertRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	email, problem := parseEmail(body.Email)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	var userID *int64
	user, err := h.authenticate(r)
	switch {
	case err == nil:
		userID = &user.ID
	case !errors.Is(err, store.ErrNotFound):
		h.fail(w, r, err)
		return
	}

	// Someone who is not signed in gets one alert: they have no identity but the
	// address they typed, so there is nothing else to hold them to it. A
	// subscriber may keep a few, and the address they give does not have to be the
	// one on their account. Staff are not held to a count.
	switch {
	case userID == nil:
		elsewhere, err := h.store.AlertElsewhere(r.Context(), email, serviceID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			h.fail(w, r, err)
			return
		}
		if elsewhere != nil {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf(
					"That address already has an alert for %s. Unsubscribe from that one first, or sign in to add more.",
					elsewhere.Name),
			})
			return
		}
	case user.Role == auth.RoleSubscriber:
		count, err := h.store.ActiveAlertCount(r.Context(), *userID, serviceID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		limit, err := h.userAlertLimit(r, *userID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if count >= limit {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf(
					"Your account has reached its limit of %d alerts. Remove one from your alerts page first.",
					limit),
			})
			return
		}
	}

	token, err := auth.NewToken()
	if err != nil {
		h.fail(w, r, err)
		return
	}

	if err := h.store.SubscribeAlert(r.Context(), serviceID, userID, email, token); err != nil {
		h.fail(w, r, err)
		return
	}

	// The answer says nothing about whether the address was already subscribed,
	// so this cannot be used to ask whether somebody else's is.
	h.log.Info("alert subscription saved", "vendor", name, "email", email, "signed_in", userID != nil)
	writeJSON(w, http.StatusCreated, map[string]any{"status": "subscribed"})
}

// getAlert is what the unsubscribe page reads before it offers the button: which
// vendor the link is about, and whether it was already used.
func (h *handlers) getAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	view, err := h.store.AlertByToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This link is not valid"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"vendorSlug":     view.VendorSlug,
		"vendorName":     view.VendorName,
		"email":          maskEmail(view.Email),
		"createdAt":      view.CreatedAt,
		"unsubscribedAt": view.UnsubscribedAt,
	})
}

// listUserAlerts is the account screen: everything this person has asked to be
// told about, including the ones they paused or ended, so they can start them
// again. The address is shown in full because it is their own screen and knowing
// which inbox is the point.
func (h *handlers) listUserAlerts(w http.ResponseWriter, r *http.Request) {
	user := h.user(r)

	alerts, err := h.store.UserAlerts(r.Context(), user.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	views := make([]map[string]any, 0, len(alerts))
	for _, alert := range alerts {
		views = append(views, map[string]any{
			"id":           alert.ID,
			"vendorSlug":   alert.VendorSlug,
			"vendorName":   alert.VendorName,
			"vendorStatus": alert.VendorStatus,
			"email":        alert.Email,
			"state":        alert.State(),
			"createdAt":    alert.CreatedAt,
		})
	}

	// The screen says how full the account is, so it can warn before the next
	// alert is refused. Only a subscriber is held to a count, so staff are told
	// nothing and the warning stays away.
	var limit *int
	used := 0
	if user.Role == auth.RoleSubscriber {
		value, err := h.userAlertLimit(r, user.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		limit = &value
		used, err = h.store.ActiveAlertCount(r.Context(), user.ID, 0)
		if err != nil {
			h.fail(w, r, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"alerts": views, "limit": limit, "used": used})
}

// userAlertLimit is how many vendors this account may watch at once. It is read
// from the account's own row, so one subscriber can differ from another; the
// environment is only the value a new account starts with.
func (h *handlers) userAlertLimit(r *http.Request, userID int64) (int, error) {
	limit, ok, err := h.store.UserLimit(r.Context(), userID, store.LimitAlert)
	if err != nil {
		return 0, err
	}
	if !ok {
		return h.alertLimit, nil
	}
	return limit, nil
}

type alertStateRequest struct {
	State string `json:"state"`
}

// setUserAlertState is pause, resume and unsubscribe, which are the same change
// of state asked for three ways.
func (h *handlers) setUserAlertState(w http.ResponseWriter, r *http.Request) {
	var body alertStateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if !store.AlertStateValid(body.State) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "State must be active, paused or unsubscribed",
		})
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Alert not found"})
		return
	}

	err = h.store.SetUserAlertState(r.Context(), h.user(r).ID, id, body.State)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Alert not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alert state changed", "user", h.user(r).Email, "alert", id, "state", body.State)
	w.WriteHeader(http.StatusNoContent)
}

// deleteUserAlertRow is the account screen's Delete: the row goes, rather than
// staying behind as an ended one.
func (h *handlers) deleteUserAlertRow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Alert not found"})
		return
	}

	err = h.store.DeleteUserAlert(r.Context(), h.user(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Alert not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alert deleted", "user", h.user(r).Email, "alert", id)
	w.WriteHeader(http.StatusNoContent)
}

// getUserAlert tells the bell whether this person already has an alert here, and
// which address it goes to. Only the account link is asked about: an alert made
// while signed out belongs to the address, and the person at the keyboard may
// not be the one who made it.
func (h *handlers) getUserAlert(w http.ResponseWriter, r *http.Request) {
	serviceID, _, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	view, err := h.store.UserAlert(r.Context(), h.user(r).ID, serviceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"subscribed": false})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"subscribed": true,
		"email":      maskEmail(view.Email),
		"paused":     view.State() == store.AlertPaused,
	})
}

// deleteUserAlert is the way out for somebody signed in. The unsubscribe link in
// an email needs a token; this needs only the account the alert was made with.
func (h *handlers) deleteUserAlert(w http.ResponseWriter, r *http.Request) {
	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	stopped, err := h.store.UnsubscribeUserAlert(r.Context(), h.user(r).ID, serviceID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alerts stopped from the vendor page", "vendor", name, "subscriptions", stopped)
	w.WriteHeader(http.StatusNoContent)
}

// maskEmail names the inbox the alerts go to without repeating the address. The
// page is reached through a link that can be forwarded, so the full one is not
// handed back to whoever holds it.
func maskEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" {
		return "your address"
	}
	return string([]rune(local)[:1]) + "***@" + domain
}

// unsubscribeAlert is the button, not the link. Mail clients and scanners fetch
// the links they are sent, so the footer link only opens the page above; this is
// what stops the alerts, and it is safe to press twice.
func (h *handlers) unsubscribeAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	err := h.store.UnsubscribeAlert(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This link is not valid"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alert unsubscribed", "token", shortToken(r.PathValue("token")))
	w.WriteHeader(http.StatusNoContent)
}

// parseEmail normalises an address and refuses anything that is not one. The
// same normalisation is used for accounts, so one address cannot subscribe twice
// by changing its case. It is shared by the alert and contact forms, which both
// take an address from a visitor.
func parseEmail(raw string) (string, string) {
	email := auth.NormalizeEmail(raw)
	if email == "" {
		return "", "An email address is required"
	}
	if len(email) > 254 {
		return "", "That email address is too long"
	}

	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || !strings.Contains(email, ".") {
		return "", "That does not look like an email address"
	}
	return email, ""
}

// shortToken is enough of a token to recognise one in a log line, and not enough
// to use.
func shortToken(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8]
}
