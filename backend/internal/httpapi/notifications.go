package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"downradar.info/backend/internal/store"
)

const (
	maxNotificationTitle = 160
	maxNotificationBody  = 4000
)

type notificationReadRequest struct {
	Read *bool `json:"read"`
}

type broadcastRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// listNotifications is the feed for the signed in account. The unread count is
// returned with it so the page and the bell agree without a second request.
func (h *handlers) listNotifications(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "The limit must be a whole number above zero",
			})
			return
		}
		limit = min(value, 100)
	}

	user := h.user(r)
	notifications, err := h.store.Notifications(r.Context(), user.ID, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	unread, err := h.store.UnreadNotifications(r.Context(), user.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": notifications,
		"count":         len(notifications),
		"unread":        unread,
	})
}

func (h *handlers) unreadNotifications(w http.ResponseWriter, r *http.Request) {
	unread, err := h.store.UnreadNotifications(r.Context(), h.user(r).ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"unread": unread})
}

func (h *handlers) setNotificationRead(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid notification id"})
		return
	}

	var body notificationReadRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil || body.Read == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	err = h.store.SetNotificationRead(r.Context(), h.user(r).ID, id, *body.Read)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Notification not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"read": *body.Read})
}

func (h *handlers) markNotificationsRead(w http.ResponseWriter, r *http.Request) {
	updated, err := h.store.SetAllNotificationsRead(r.Context(), h.user(r).ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

// sendBroadcast is the staff side of the same feed: one announcement written
// once and handed to every active subscriber account.
func (h *handlers) sendBroadcast(w http.ResponseWriter, r *http.Request) {
	var body broadcastRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	title := clean(sanitize(body.Title))
	text := clean(sanitize(body.Body))
	if title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A title is required"})
		return
	}
	if utf8.RuneCountInString(title) > maxNotificationTitle {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "The title is too long",
		})
		return
	}
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A message is required"})
		return
	}
	if utf8.RuneCountInString(text) > maxNotificationBody {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "The message is too long",
		})
		return
	}

	user := h.user(r)
	sent, err := h.store.CreateNotification(r.Context(), store.NotificationInput{
		Audience:  store.AudienceSubscribers,
		Title:     title,
		Body:      text,
		CreatedBy: &user.ID,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": sent})
}
