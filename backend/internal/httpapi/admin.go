package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/store"
)

const (
	visibilityPublic  = "public"
	visibilityPrivate = "private"
)

// servicePayload carries only the fields the caller sent. A nil field is left
// alone, which is what makes the same struct work for create and for patch.
type servicePayload struct {
	Name       *string   `json:"name"`
	URL        *string   `json:"url"`
	Profile    *string   `json:"profile"`
	Category   *string   `json:"category"`
	LogoURL    *string   `json:"logoUrl"`
	Endpoint   *string   `json:"endpoint"`
	Aliases    *[]string `json:"aliases"`
	Status     *string   `json:"status"`
	Active     *bool     `json:"active"`
	Visibility *string   `json:"visibility"`
}

func decodeServicePayload(w http.ResponseWriter, r *http.Request) (*servicePayload, bool) {
	var body servicePayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return nil, false
	}
	return &body, true
}

// viewer turns the session into the question the store asks: what may this
// person see?
func (h *handlers) viewer(r *http.Request) store.Viewer {
	user := h.user(r)
	if user == nil {
		return store.Public()
	}
	return store.Viewer{UserID: user.ID, IsStaff: auth.CanManageServices(user.Role)}
}

func (h *handlers) listProfiles(w http.ResponseWriter, r *http.Request) {
	type profileView struct {
		Name                  string `json:"name"`
		Kind                  string `json:"kind"`
		Endpoint              string `json:"endpoint,omitempty"`
		PollIntervalSeconds   int    `json:"pollIntervalSeconds"`
		ActiveIntervalSeconds int    `json:"activeIntervalSeconds"`
	}

	out := make([]profileView, 0, len(h.profiles))
	for name, p := range h.profiles {
		out = append(out, profileView{
			Name:                  name,
			Kind:                  p.Kind,
			Endpoint:              p.Endpoint,
			PollIntervalSeconds:   p.PollIntervalSeconds,
			ActiveIntervalSeconds: p.ActiveIntervalSeconds,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

func (h *handlers) listCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := h.store.DistinctCategories(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if categories == nil {
		categories = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

const maxSearchLength = 100

func (h *handlers) listServices(w http.ResponseWriter, r *http.Request) {
	// A search box is not worth an error message, so a long term is cut short
	// rather than refused.
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	if runes := []rune(search); len(runes) > maxSearchLength {
		search = string(runes[:maxSearchLength])
	}

	services, err := h.store.AdminListServices(
		r.Context(), r.URL.Query().Get("filter"), search, h.viewer(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if services == nil {
		services = []store.AdminService{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"services": services,
		"count":    len(services),
	})
}

// findVisibleService reads a service and answers 404 when the caller may not see
// it. Not 403: a private service should not even be confirmed to exist.
func (h *handlers) findVisibleService(w http.ResponseWriter, r *http.Request) *store.AdminService {
	current, err := h.store.AdminGetService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
		return nil
	}
	if err != nil {
		h.fail(w, r, err)
		return nil
	}
	if !h.viewer(r).CanSee(current.OwnerID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
		return nil
	}
	return current
}

// myServices is the dashboard's own list: the private services of the caller, in
// the public shape, so the browser can add them to the catalogue. It is a
// separate route from the management list because the two answer different
// questions and return different shapes.
func (h *handlers) myServices(w http.ResponseWriter, r *http.Request) {
	vendors, err := h.store.OwnedVendors(r.Context(), h.user(r).ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if vendors == nil {
		vendors = []store.VendorSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vendors": vendors,
		"count":   len(vendors),
	})
}

func (h *handlers) getService(w http.ResponseWriter, r *http.Request) {
	current := h.findVisibleService(w, r)
	if current == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"service": current})
}

// serviceStatus is the detail view for a private service, which has no page of
// its own on the public site.
func (h *handlers) serviceStatus(w http.ResponseWriter, r *http.Request) {
	detail, err := h.store.GetVendor(r.Context(), r.PathValue("slug"), h.viewer(r))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *handlers) createService(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeServicePayload(w, r)
	if !ok {
		return
	}

	user := h.user(r)
	name := ""
	if body.Name != nil {
		name = clean(sanitize(*body.Name))
	}
	pageURL := clean(sanitize(stringValue(body.URL)))
	profile := clean(sanitize(stringValue(body.Profile)))
	category := clean(sanitize(stringValue(body.Category)))
	logoURL := clean(sanitize(stringValue(body.LogoURL)))
	endpoint := clean(sanitize(stringValue(body.Endpoint)))
	aliases := cleanAliases(body.Aliases)

	// Private is the default, so a caller that forgets the field ends up with a
	// service only they can see, rather than one published to everybody.
	visibility := visibilityPrivate
	if body.Visibility != nil {
		visibility = strings.ToLower(clean(sanitize(*body.Visibility)))
	}
	if visibility != visibilityPublic && visibility != visibilityPrivate {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "visibility must be public or private",
		})
		return
	}
	if visibility == visibilityPublic && !auth.CanManageServices(user.Role) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "only staff can add a service to the public catalogue",
		})
		return
	}

	// A service added here goes live immediately, so it has to be pollable.
	status := string(store.StatusPublished)
	if problem := h.validateService(name, pageURL, profile, category, logoURL, aliases, status); problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	var ownerID *int64
	if visibility == visibilityPrivate {
		ownerID = &user.ID
	}

	chosen := h.profiles[profile]
	slug, err := h.store.CreateService(r.Context(), store.NewService{
		Name:                  name,
		URL:                   pageURL,
		Profile:               profile,
		Category:              category,
		LogoURL:               logoURL,
		Endpoint:              endpoint,
		Aliases:               aliases,
		PollIntervalSeconds:   chosen.PollIntervalSeconds,
		ActiveIntervalSeconds: chosen.ActiveIntervalSeconds,
		OwnerID:               ownerID,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("service added", "slug", slug, "visibility", visibility, "by", user.Email)
	writeJSON(w, http.StatusCreated, map[string]any{
		"slug":       slug,
		"status":     status,
		"visibility": visibility,
	})
}

func (h *handlers) updateService(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	current := h.findVisibleService(w, r)
	if current == nil {
		return
	}

	body, ok := decodeServicePayload(w, r)
	if !ok {
		return
	}
	if body.Name == nil && body.URL == nil && body.Profile == nil && body.Category == nil &&
		body.LogoURL == nil && body.Endpoint == nil && body.Aliases == nil &&
		body.Status == nil && body.Active == nil && body.Visibility == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nothing to update"})
		return
	}

	user := h.user(r)

	// The row has to stay valid as a whole, so the patch is merged onto the
	// stored values before it is checked. Publishing a proposal that has no url
	// or profile is exactly what this catches.
	name := current.Name
	pageURL := current.URL
	profile := current.Profile
	category := current.Category
	logoURL := current.LogoURL
	endpoint := current.Endpoint
	aliases := current.Aliases
	status := current.Status

	if body.Name != nil {
		name = clean(sanitize(*body.Name))
	}
	if body.URL != nil {
		pageURL = clean(sanitize(*body.URL))
	}
	if body.Profile != nil {
		profile = clean(sanitize(*body.Profile))
	}
	if body.Category != nil {
		category = clean(sanitize(*body.Category))
	}
	if body.LogoURL != nil {
		logoURL = clean(sanitize(*body.LogoURL))
	}
	if body.Endpoint != nil {
		endpoint = clean(sanitize(*body.Endpoint))
	}
	if body.Aliases != nil {
		aliases = cleanAliases(body.Aliases)
	}
	if body.Status != nil {
		status = strings.ToLower(clean(sanitize(*body.Status)))
	}

	if problem := h.validateService(name, pageURL, profile, category, logoURL, aliases, status); problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	patch := store.ServicePatch{Active: body.Active}
	if body.Name != nil {
		patch.Name = &name
	}
	if body.URL != nil {
		patch.URL = &pageURL
	}
	if body.Profile != nil {
		patch.Profile = &profile
	}
	if body.Category != nil {
		patch.Category = &category
	}
	if body.LogoURL != nil {
		patch.LogoURL = &logoURL
	}
	if body.Endpoint != nil {
		patch.Endpoint = &endpoint
	}
	if body.Aliases != nil {
		patch.Aliases = &aliases
	}
	if body.Status != nil {
		patch.Status = &status
	}

	// Moving a service between the catalogue and one person's list is a staff
	// decision, and a private service belongs to whoever asked for it.
	if body.Visibility != nil {
		if !auth.CanManageServices(user.Role) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "only staff can change who a service belongs to",
			})
			return
		}
		visibility := strings.ToLower(clean(sanitize(*body.Visibility)))
		if visibility != visibilityPublic && visibility != visibilityPrivate {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "visibility must be public or private",
			})
			return
		}
		patch.SetOwner = true
		if visibility == visibilityPrivate {
			patch.OwnerID = &user.ID
		}
	}

	if err := h.store.UpdateService(r.Context(), slug, patch); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
			return
		}
		h.fail(w, r, err)
		return
	}

	updated, err := h.store.AdminGetService(r.Context(), slug)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("service updated", "slug", slug, "status", status, "by", user.Email)
	writeJSON(w, http.StatusOK, map[string]any{"service": updated})
}

func (h *handlers) canDeleteService(current *store.AdminService, user *store.User, viewer store.Viewer) bool {
	if viewer.Owns(current.OwnerID) {
		return true
	}
	if current.Status == string(store.StatusProposed) {
		return auth.CanManageServices(user.Role)
	}
	return auth.CanRemovePublished(user.Role)
}

func (h *handlers) deleteService(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	current := h.findVisibleService(w, r)
	if current == nil {
		return
	}

	user := h.user(r)
	if !h.canDeleteService(current, user, h.viewer(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "only an admin can remove a published service that is not yours",
		})
		return
	}

	outcome, err := h.store.DeleteService(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
			return
		}
		h.fail(w, r, err)
		return
	}

	h.log.Info("service deleted", "slug", slug, "outcome", outcome, "by", user.Email)
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "outcome": outcome})
}

func (h *handlers) validateService(name, pageURL, profile, category, logoURL string, aliases []string, status string) string {
	if n := utf8.RuneCountInString(name); n < 2 || n > 80 {
		return "service name must be between 2 and 80 characters"
	}
	if !store.Status(status).Valid() {
		return "status must be proposed, published or rejected"
	}
	if status == string(store.StatusPublished) {
		if pageURL == "" {
			return "a published service needs a status page url"
		}
		if profile == "" {
			return "a published service needs an adapter profile"
		}
	}
	if pageURL != "" {
		if err := catalog.ValidateURL(pageURL); err != nil {
			return err.Error()
		}
	}
	if profile != "" {
		if _, ok := h.profiles[profile]; !ok {
			return fmt.Sprintf("unknown profile %q", profile)
		}
	}
	if utf8.RuneCountInString(category) > 40 {
		return "category must be at most 40 characters"
	}
	if logoURL != "" {
		if err := catalog.ValidateURL(logoURL); err != nil {
			return "logo url " + err.Error()
		}
	}
	if len(aliases) > 20 {
		return "at most 20 aliases are allowed"
	}
	for _, alias := range aliases {
		if n := utf8.RuneCountInString(alias); n < 2 || n > 60 {
			return "each alias must be between 2 and 60 characters"
		}
	}
	return ""
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func cleanAliases(in *[]string) []string {
	if in == nil {
		return []string{}
	}
	out := make([]string, 0, len(*in))
	for _, alias := range *in {
		if trimmed := clean(sanitize(alias)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
