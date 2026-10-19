package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

// webhookRequest is what a request says about the second way an alert can
// arrive: the URL to call, the method, and the headers to send with it. An empty
// URL is how a request stops the calls; leaving the whole thing out is how the
// bell, which knows nothing about webhooks, leaves one alone.
//
// There is nothing here about a body. What a call carries is written by the
// sender from what it finds, so a receiver reads the same fields every time.
type webhookRequest struct {
	URL     string             `json:"url"`
	Method  string             `json:"method"`
	Headers []store.AlertField `json:"headers"`
}

type alertRequest struct {
	// Email is the address the alert should go to. An empty one stops the emails;
	// a request that says nothing about it, which is what the bell sends, leaves
	// the email way as it is.
	Email *string `json:"email"`

	// Webhook is the other way an alert can arrive. A page that shows both ways
	// sends it every time; the bell sends nothing about it.
	Webhook *webhookRequest `json:"webhook"`

	// What the email reports on. WholeService is the vendor's own status;
	// Components names components by the identifiers the vendor page shows,
	// which is what the person ticking the boxes has to hand. A request that
	// names neither is the alert as it used to be, the vendor's overall status:
	// the bell only ever sent that, and a page left open from before this sends
	// it still. A webhook ignores both, which is why a request with a webhook
	// alone needs no pick.
	WholeService *bool    `json:"wholeService"`
	Components   []string `json:"components"`
}

// subscribeAlert takes what somebody wants to hear about one vendor. No account
// is needed for an address, which is the point: an alert by email is the address.
// When the visitor happens to be signed in their id is recorded with it, so the
// row can be tied to them later without asking again, and they are free to give
// any address they like.
//
// A webhook is different: it hangs off an account rather than off an address, so
// it takes a subscriber, and it hears about every change of the vendor rather
// than about a pick of its components, because the body of a call says what the
// vendor looks like now. Everything from a vendor page is one alert per vendor,
// which is why a save writes the ways it names and leaves the rest alone.
//
// Asking again is changing their mind: what the subscription watches becomes what
// this request names, so a page can save the whole selection.
func (h *handlers) subscribeAlert(w http.ResponseWriter, r *http.Request) {
	if !h.alertsEnabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Alerts are not available right now",
		})
		return
	}

	// The body holds a component list and a handful of headers.
	var body alertRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	// The address is optional: an alert can arrive by webhook alone, and a request
	// that clears it is somebody saying they would rather not be emailed. A
	// request that says nothing about it is the bell, which only ever had one.
	address := ""
	saveEmail := body.Email != nil
	if body.Email != nil && strings.TrimSpace(*body.Email) != "" {
		parsed, problem := parseEmail(*body.Email)
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		address = parsed
	}

	var hook *store.AlertWebhook
	if body.Webhook != nil {
		parsed, problem := parseWebhook(*body.Webhook)
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		hook = parsed
	}

	// A request that names no way at all would write a subscription with nothing
	// to send, which is not an alert. Clearing a way is naming it: an empty
	// address is how somebody stops the emails.
	if body.Email == nil && body.Webhook == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "An email address or a webhook is required",
		})
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

	// A request that names neither is the older shape of this request, and it
	// meant the vendor's own status: the bell only ever sent that, and a page
	// left open from before this sends it still. Naming an empty list is naming
	// nothing, which an address is told about below rather than read as the whole
	// vendor.
	whole := body.WholeService == nil && body.Components == nil
	if body.WholeService != nil {
		whole = *body.WholeService
	}

	componentIDs, err := h.store.ComponentIDs(r.Context(), serviceID, body.Components)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "One of those components is not part of this service any more. Reload the page and pick again.",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// An email reports on what was picked, so an address with nothing picked
	// beside it would be a way that never has anything to say. A webhook needs no
	// pick of its own: it hears about the vendor itself.
	if saveEmail && address != "" && !whole && len(componentIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Pick at least one thing to be emailed about",
		})
		return
	}

	var userID *int64
	var role string
	user, err := h.authenticate(r)
	switch {
	case err == nil:
		userID = &user.ID
		role = user.Role
	case !errors.Is(err, store.ErrNotFound):
		h.fail(w, r, err)
		return
	}

	calls := hook != nil && hook.URL != ""

	// An empty address from somebody with no account says nothing: there is no
	// held alert to stop, and the address is the only thing they have. A
	// subscriber clears an address to stop the emails that go to it.
	if userID == nil && saveEmail && address == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "An email address is required"})
		return
	}

	// Someone who is not signed in gets one alert: they have no identity but the
	// address they typed, so there is nothing else to hold them to it. That alert
	// is a whole vendor, because a component is a subscriber's, and it cannot
	// call anywhere either, because a webhook is kept on an account. A subscriber
	// may watch a few vendors, and the address they give does not have to be the
	// one on their account. Staff are not held to a count.
	switch {
	case userID == nil:
		if len(componentIDs) > 0 {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Components are for subscribers. Sign in to follow one.",
			})
			return
		}
		if calls {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Webhooks are for subscribers. Sign in to add one.",
			})
			return
		}
		elsewhere, err := h.store.AlertElsewhere(r.Context(), address, serviceID)
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
	case role == auth.RoleSubscriber:
		// The cap counts vendors rather than the things watched inside one, so
		// this request is asking for a single place in it, unless the alert it
		// writes is already there: changing an address, adding a webhook beside
		// one or stopping a way is being replaced rather than added, and no place
		// is taken.
		watched, err := h.store.WatchedServiceCount(r.Context(), *userID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		already := false
		if address != "" {
			already, err = h.store.AddressWatchesService(r.Context(), address, serviceID)
			if err != nil {
				h.fail(w, r, err)
				return
			}
		}
		if !already {
			already, err = h.store.UserWatchesService(r.Context(), *userID, serviceID)
			if err != nil {
				h.fail(w, r, err)
				return
			}
		}
		adding := 1
		if already {
			adding = 0
		}
		limit, err := h.userAlertLimit(r, *userID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if watched+adding > limit {
			services := "services"
			if limit == 1 {
				services = "service"
			}
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf(
					"Your account has reached its limit of %d %s. Remove one from your alerts page first.",
					limit, services),
			})
			return
		}
	}

	// Each channel a save creates takes a token of its own, which is what stops
	// that way and what a webhook is checked against. One that is already there
	// keeps its token.
	change := store.AlertChange{Targets: store.AlertTargets{Whole: whole, Components: componentIDs}}
	if saveEmail {
		token, err := auth.NewToken()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		change.Email, change.EmailToken = &address, token
	}
	if hook != nil {
		token, err := auth.NewToken()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		change.Webhook, change.WebhookToken = hook, token
	}

	if err := h.store.SubscribeAlert(r.Context(), serviceID, userID, change); err != nil {
		h.fail(w, r, err)
		return
	}

	// The answer says nothing about whether the address was already subscribed,
	// so this cannot be used to ask whether somebody else's is.
	h.log.Info("alert subscription saved", "vendor", name, "email", address != "" && saveEmail,
		"webhook", calls, "signed_in", userID != nil, "whole", whole, "components", len(componentIDs))
	writeJSON(w, http.StatusCreated, map[string]any{"status": "subscribed"})
}

// webhookMethods are the methods a webhook can be sent with. They are the ones
// that carry a body, which is what a call is: a GET has nothing to say and would
// arrive with the news missing.
var webhookMethods = map[string]bool{
	http.MethodPost:  true,
	http.MethodPut:   true,
	http.MethodPatch: true,
}

// parseWebhook reads what a request says about a webhook and refuses anything the
// sender could not use. Nothing here reads inside a header value: it may carry
// placeholders, and what one turns into is the sender's business.
func parseWebhook(body webhookRequest) (*store.AlertWebhook, string) {
	rawURL := strings.TrimSpace(body.URL)
	if rawURL == "" {
		// An empty URL is how a request stops the calls.
		return &store.AlertWebhook{}, ""
	}
	if len(rawURL) > 1024 {
		return nil, "That webhook URL is too long"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, "That does not look like a webhook URL"
	}

	method := strings.ToUpper(strings.TrimSpace(body.Method))
	if method == "" {
		method = http.MethodPost
	}
	if !webhookMethods[method] {
		return nil, "The method must be POST, PUT or PATCH"
	}

	headers, problem := parseFields(body.Headers, "header")
	if problem != "" {
		return nil, problem
	}
	for _, header := range headers {
		if strings.ContainsAny(header.Name+header.Value, "\r\n") {
			return nil, "A header cannot hold a line break"
		}
	}

	return &store.AlertWebhook{URL: rawURL, Method: method, Headers: headers}, ""
}

// parseFields reads a list of names and values, and drops the empty rows a screen
// can leave behind. A row with a value but no name has nowhere to go, so it is
// refused rather than sent as an empty name.
func parseFields(in []store.AlertField, what string) ([]store.AlertField, string) {
	out := make([]store.AlertField, 0, len(in))
	for _, field := range in {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			if strings.TrimSpace(field.Value) == "" {
				continue
			}
			return nil, fmt.Sprintf("A %s needs a name", what)
		}
		out = append(out, store.AlertField{Name: name, Value: field.Value})
	}
	return out, ""
}

// testWebhook makes one call with what the page is holding, saved or not, and
// answers with what came back, so a subscriber can watch their own end work
// before they lean on it. A far end that refuses is an answer rather than a
// failure of this endpoint: the status and the body are handed back as they came,
// and a call that never got there says so in error.
//
// The call is recorded like a delivery, so the page can show it beside the ones
// that went out on their own.
func (h *handlers) testWebhook(w http.ResponseWriter, r *http.Request) {
	if !h.alertsEnabled || h.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Alerts are not available right now",
		})
		return
	}

	var body webhookRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	hook, problem := parseWebhook(body)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	if hook.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A webhook URL is required"})
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

	result, _ := h.notifier.TestWebhook(r.Context(), serviceID, *hook)

	user := h.user(r)
	if err := h.store.LogWebhookCall(r.Context(), &user.ID, serviceID, nil, store.WebhookCall{
		Kind:     store.CallTest,
		URL:      hook.URL,
		Method:   hook.Method,
		Status:   statusOrNil(result.Status),
		Response: result.Response,
		Error:    result.Error,
	}); err != nil {
		h.log.Error("failed to record a webhook try", "error", err)
	}

	h.log.Info("webhook tried from the alert page", "vendor", name, "user", user.Email,
		"status", result.Status, "error", result.Error)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   statusOrNil(result.Status),
		"response": result.Response,
		"error":    result.Error,
	})
}

// statusOrNil writes "no answer" as a null rather than a zero, so a call that was
// never answered does not read as one that got a 0.
func statusOrNil(status int) *int {
	if status == 0 {
		return nil
	}
	return &status
}

// alertPauseRequest is one way of one alert being stopped or started. It names a
// way rather than a row, because what it changes is that way and nothing else.
type alertPauseRequest struct {
	Kind   string `json:"kind"`
	Paused bool   `json:"paused"`
}

// setAlertChannelPaused stops one way of an alert arriving, or starts it again,
// without taking away the address or the URL it was told: stopping a way for good
// is clearing that field and saving, which is what the page's own field does.
func (h *handlers) setAlertChannelPaused(w http.ResponseWriter, r *http.Request) {
	var body alertPauseRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if body.Kind != store.ChannelEmail && body.Kind != store.ChannelWebhook {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Kind must be email or webhook",
		})
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

	err = h.store.SetChannelPaused(r.Context(), h.user(r).ID, serviceID, body.Kind, body.Paused)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This alert has no such way"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alert way paused", "vendor", name, "user", h.user(r).Email,
		"way", body.Kind, "paused", body.Paused)
	w.WriteHeader(http.StatusNoContent)
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
			"wholeService": alert.WholeService,
			"components":   alert.Components,
			"channels":     alert.Channels,
		})
	}

	// The screen says how full the account is, so it can warn before the next
	// vendor is refused. Only a subscriber is held to a count, so staff are told
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
		used, err = h.store.WatchedServiceCount(r.Context(), user.ID)
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

// getUserAlert is what the alert page reads before it draws: whether this person
// already has an alert here, the way each of its channels goes, whether it is
// paused, and what it watches, so the page opens with what they left. The address
// is shown in full because it is their own screen and the page edits it; only the
// account link is asked about, because an alert made while signed out belongs to
// the address and the person at the keyboard may not be the one who made it.
//
// Each way says whether it is quiet on its own, since one of them can be paused
// without the other, and the last calls of the webhook come with it so the page
// can show what it did rather than only what it is set to.
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

	// The calls are read whether or not there is an alert: a webhook can be tried
	// from the page before anything is saved, and what it answered is worth
	// showing the next time the page opens.
	calls, err := h.store.WebhookCalls(r.Context(), h.user(r).ID, serviceID, 10)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	view, err := h.store.UserAlert(r.Context(), h.user(r).ID, serviceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"subscribed": false, "calls": calls})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"subscribed":    true,
		"email":         view.Email,
		"paused":        view.State() == store.AlertPaused,
		"emailPaused":   view.EmailPaused,
		"webhookPaused": view.WebhookPaused,
		"wholeService":  view.WholeService,
		"components":    view.ComponentIDs,
		"webhook":       view.Webhook,
		"calls":         calls,
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
