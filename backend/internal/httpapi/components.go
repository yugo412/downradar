package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"downradar.info/backend/internal/store"
)

// componentHiddenRequest names one component and whether the public page should
// show it. The id is the source's own id, which is what the page already holds.
type componentHiddenRequest struct {
	SourceID string `json:"sourceId"`
	Hidden   bool   `json:"hidden"`
}

// listComponents returns every component of a service for staff, hidden ones
// included, so the vendor page can offer to show one again.
func (h *handlers) listComponents(w http.ResponseWriter, r *http.Request) {
	components, err := h.store.ServiceComponents(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Service not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"components": components})
}

// setComponentHidden takes one component off the public vendor page, or puts it
// back. The component keeps being polled and keeps its state either way.
func (h *handlers) setComponentHidden(w http.ResponseWriter, r *http.Request) {
	var body componentHiddenRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	sourceID := strings.TrimSpace(body.SourceID)
	if sourceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a component id is required"})
		return
	}

	slug := r.PathValue("slug")
	if err := h.store.SetComponentHidden(r.Context(), slug, sourceID, body.Hidden); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Component not found"})
		return
	} else if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("component visibility changed", "vendor", slug, "component", sourceID,
		"hidden", body.Hidden, "user", h.user(r).Email)
	writeJSON(w, http.StatusOK, map[string]any{"sourceId": sourceID, "hidden": body.Hidden})
}
