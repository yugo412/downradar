package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/detect"
	"downradar.info/backend/internal/store"
)

const (
	visibilityPublic  = "public"
	visibilityPrivate = "private"
)

// detectionBudget bounds one detection request end to end, which keeps it
// inside the server's write timeout even if the queue grows. The detector bounds
// each profile it tries as well, so this is the backstop for the whole queue.
const detectionBudget = 20 * time.Second

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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
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

// profileView is how a profile is shown to the management screens: what it is
// called, which platform it reads, and what that means in one line.
type profileView struct {
	Name                  string `json:"name"`
	Kind                  string `json:"kind"`
	Description           string `json:"description,omitempty"`
	Endpoint              string `json:"endpoint,omitempty"`
	PollIntervalSeconds   int    `json:"pollIntervalSeconds"`
	ActiveIntervalSeconds int    `json:"activeIntervalSeconds"`
	Visible               bool   `json:"visible"`
}

func profileViewOf(name string, p catalog.Profile) profileView {
	return profileView{
		Name:                  name,
		Kind:                  p.Kind,
		Description:           p.Description,
		Endpoint:              p.Endpoint,
		PollIntervalSeconds:   p.PollIntervalSeconds,
		ActiveIntervalSeconds: p.ActiveIntervalSeconds,
		Visible:               p.Visible,
	}
}

func (h *handlers) listProfiles(w http.ResponseWriter, r *http.Request) {
	out := make([]profileView, 0, len(h.catalog.Profiles))
	for name, p := range h.catalog.Profiles {
		out = append(out, profileViewOf(name, p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

// noMatchMessage is what a caller is told when the queue ran out of profiles. It
// names both ways forward, because from here it is not possible to tell a wrong
// url from a platform that has no adapter yet.
const noMatchMessage = "No adapter profile could read this page. Check that the url is the status page itself; if it is, a new profile has to be written for its platform before the service can be polled."

// detectProfile answers which profile reads a page, so the picker can be filled
// in before anything is saved.
func (h *handlers) detectProfile(w http.ResponseWriter, r *http.Request) {
	var body detectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	pageURL := clean(sanitize(body.URL))
	if pageURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "A status page url is needed to detect a profile",
		})
		return
	}
	if err := catalog.ValidateURL(pageURL); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	match, err := h.detectPage(r, clean(sanitize(body.Name)), pageURL)
	if err != nil {
		var noMatch *detect.NoMatch
		if !errors.As(err, &noMatch) {
			h.log.Error("profile detection failed", "url", pageURL, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "The page could not be checked, try again in a moment",
			})
			return
		}
		h.log.Info("no profile matched", "url", pageURL, "by", h.user(r).Email, "error", err)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": noMatchMessage})
		return
	}

	h.log.Info("profile detected", "url", pageURL, "profile", match.Profile, "by", h.user(r).Email)
	writeJSON(w, http.StatusOK, map[string]any{
		"profile": profileViewOf(match.Profile, h.catalog.Profiles[match.Profile]),
	})
}

type detectRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// detectProfileFor runs the queue for a vendor that arrived without a profile.
// An empty answer means nothing matched, which the callers store as no_match.
func (h *handlers) detectProfileFor(r *http.Request, name, pageURL string) string {
	match, err := h.detectPage(r, name, pageURL)
	if err != nil {
		var noMatch *detect.NoMatch
		if errors.As(err, &noMatch) {
			h.log.Info("no profile matched, the vendor is saved without one", "url", pageURL, "error", err)
		} else {
			h.log.Error("profile detection failed", "url", pageURL, "error", err)
		}
		return ""
	}

	h.log.Info("profile detected", "url", pageURL, "profile", match.Profile)
	return match.Profile
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Service not found"})
		return nil
	}
	if err != nil {
		h.fail(w, r, err)
		return nil
	}
	if !h.viewer(r).CanSee(current.OwnerID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Service not found"})
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Service not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// pollLogLimit is how much of the history the detail page shows. The poller
// writes a row per attempt, so a vendor polled every five minutes makes 288 a
// day; the page wants the recent ones, not the year.
const pollLogLimit = 50

// staffOrOwner is who may see the parts of a service that are not the vendor's
// public face: the fetch log, and running the detect queue by hand. A public
// vendor is visible to every signed in visitor, so those need their own check.
func staffOrOwner(viewer store.Viewer, ownerID *int64) bool {
	return viewer.IsStaff || viewer.Owns(ownerID)
}

// pollLog is the fetch history of one service: when it was read, what came
// back, and what did not parse.
func (h *handlers) pollLog(w http.ResponseWriter, r *http.Request) {
	current := h.findVisibleService(w, r)
	if current == nil {
		return
	}
	if !staffOrOwner(h.viewer(r), current.OwnerID) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Only an admin, a moderator or the owner can read the fetch log",
		})
		return
	}

	runs, err := h.store.RecentPollRuns(r.Context(), current.Slug, pollLogLimit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"polls": runs})
}

// detectPage offers one page to the queue. The budget is what keeps a request
// inside the server's write timeout, and it belongs here rather than with the
// callers because every path that reads a page is bounded the same way.
func (h *handlers) detectPage(r *http.Request, name, pageURL string) (detect.Match, error) {
	ctx, cancel := context.WithTimeout(r.Context(), detectionBudget)
	defer cancel()

	return h.detector.Detect(ctx, name, pageURL)
}

// retryDetection runs the queue again for a vendor that has no profile, which is
// what turns it into a polled vendor as soon as a profile for its platform is
// deployed. It is the same work the poller does once an hour, done on demand.
func (h *handlers) retryDetection(w http.ResponseWriter, r *http.Request) {
	current := h.findVisibleService(w, r)
	if current == nil {
		return
	}
	if !staffOrOwner(h.viewer(r), current.OwnerID) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Only an admin, a moderator or the owner can retry detection",
		})
		return
	}
	switch {
	case !current.Active:
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "This service is removed, restore it before retrying",
		})
		return
	case current.Profile != "":
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "This service already has an adapter profile",
		})
		return
	case current.Status != string(store.StatusNoMatch):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Only a service that no profile matched can be retried",
		})
		return
	case current.URL == "":
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "A status page url is needed to detect a profile",
		})
		return
	}

	match, err := h.detectPage(r, current.Name, current.URL)
	if err != nil {
		var noMatch *detect.NoMatch
		if !errors.As(err, &noMatch) {
			h.log.Error("profile detection failed", "slug", current.Slug, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "The page could not be checked, try again in a moment",
			})
			return
		}
		h.log.Info("no profile matched", "slug", current.Slug, "by", h.user(r).Email, "error", err)
		if parkErr := h.store.ParkDetection(r.Context(), current.ID, err.Error()); parkErr != nil {
			h.fail(w, r, parkErr)
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": noMatchMessage})
		return
	}

	profile, ok := h.catalog.Profiles[match.Profile]
	if !ok {
		h.log.Error("detected a profile that is not declared", "slug", current.Slug, "profile", match.Profile)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "The profile that matched is not available",
		})
		return
	}

	if err := h.store.SetDetectedProfile(r.Context(), current.ID, match.Profile,
		profile.PollIntervalSeconds, profile.ActiveIntervalSeconds); err != nil {
		h.fail(w, r, err)
		return
	}
	h.log.Info("profile detected", "slug", current.Slug, "profile", match.Profile, "by", h.user(r).Email)

	if _, err := h.store.ApplyReport(r.Context(), current.ID, match.Report); err != nil {
		h.log.Warn("the detected profile could not store its first report",
			"slug", current.Slug, "profile", match.Profile, "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{"profile": profileViewOf(match.Profile, profile)})
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
			"error": "Visibility must be public or private",
		})
		return
	}
	if visibility == visibilityPublic && !auth.CanManageServices(user.Role) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Only staff can add a service to the public catalogue",
		})
		return
	}

	// A vendor added without a profile is offered to the detection queue, so the
	// platform does not have to be known up front. When nothing matches it is
	// still saved, as no_match, and the poller keeps looking.
	status := string(store.StatusPublished)
	if profile == "" && pageURL != "" {
		if profile = h.detectProfileFor(r, name, pageURL); profile == "" {
			status = string(store.StatusNoMatch)
		}
	}

	// A service added here goes live immediately, so it has to be pollable.
	if problem := h.validateService(name, pageURL, profile, category, logoURL, aliases, status); problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	var ownerID *int64
	if visibility == visibilityPrivate {
		ownerID = &user.ID
	}

	chosen := h.catalog.Profiles[profile]
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
		Status:                status,
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
		"profile":    profile,
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Nothing to update"})
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

	// A vendor that is about to be polled without a profile is offered to the
	// detection queue, the same way the add form does it. When nothing matches it
	// is parked as no_match rather than left in a state nothing can poll.
	if status == string(store.StatusPublished) && profile == "" && pageURL != "" {
		if profile = h.detectProfileFor(r, name, pageURL); profile == "" {
			status = string(store.StatusNoMatch)
		}
	}

	// A parked vendor that has just been given a profile is pollable again, so it
	// goes live instead of staying in the one state the poller leaves alone.
	if status == string(store.StatusNoMatch) && profile != "" {
		status = string(store.StatusPublished)
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

	// The intervals are declared by the profile, so they follow it. A vendor
	// parked as no_match picks up the cadence of the profile that matched it the
	// moment the profile is written.
	if profile != current.Profile {
		patch.Profile = &profile
		if chosen, ok := h.catalog.Profiles[profile]; ok {
			patch.PollIntervalSeconds = &chosen.PollIntervalSeconds
			patch.ActiveIntervalSeconds = &chosen.ActiveIntervalSeconds
		}
	}
	if status != current.Status {
		patch.Status = &status
	}

	// Moving a service between the catalogue and one person's list is a staff
	// decision, and a private service belongs to whoever asked for it.
	if body.Visibility != nil {
		if !auth.CanManageServices(user.Role) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "Only staff can change who a service belongs to",
			})
			return
		}
		visibility := strings.ToLower(clean(sanitize(*body.Visibility)))
		if visibility != visibilityPublic && visibility != visibilityPrivate {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Visibility must be public or private",
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
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Service not found"})
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

	// A force delete is for a row that was already taken off the site: it wipes
	// the row and everything recorded against it, so it is an admin's alone.
	if r.URL.Query().Get("force") == "true" {
		h.purgeService(w, r, current, user)
		return
	}

	if !h.canDeleteService(current, user, h.viewer(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Only an admin can remove a published service that is not yours",
		})
		return
	}

	outcome, err := h.store.DeleteService(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Service not found"})
			return
		}
		h.fail(w, r, err)
		return
	}

	h.log.Info("service deleted", "slug", slug, "outcome", outcome, "by", user.Email)
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "outcome": outcome})
}

// purgeService deletes a removed row for good, history and all. A service that is
// still on the site has to be removed first, so the two steps stay apart in the
// log and in somebody's head.
func (h *handlers) purgeService(w http.ResponseWriter, r *http.Request, current *store.AdminService, user *store.User) {
	if !auth.CanPurgeServices(user.Role) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Only an admin can delete a service for good",
		})
		return
	}

	if current.Active {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "Remove the service before deleting it for good",
		})
		return
	}

	if err := h.store.PurgeService(r.Context(), current.Slug); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Service not found"})
			return
		}
		h.fail(w, r, err)
		return
	}

	h.log.Info("service purged", "slug", current.Slug, "by", user.Email)
	writeJSON(w, http.StatusOK, map[string]any{
		"slug": current.Slug, "outcome": store.DeletePermanently,
	})
}

func (h *handlers) validateService(name, pageURL, profile, category, logoURL string, aliases []string, status string) string {
	if n := utf8.RuneCountInString(name); n < 2 || n > 80 {
		return "Service name must be between 2 and 80 characters"
	}
	if !store.Status(status).Valid() {
		return "Status must be proposed, published, rejected or no_match"
	}
	if status == string(store.StatusPublished) {
		if pageURL == "" {
			return "A published service needs a status page url"
		}
		if profile == "" {
			return "A published service needs an adapter profile"
		}
	}
	// A vendor that no profile matched is kept without one, but it is still a
	// page: with no url there would be nothing for the poller to try again.
	if status == string(store.StatusNoMatch) && pageURL == "" {
		return "A service with no matched profile needs a status page url"
	}
	if pageURL != "" {
		if err := catalog.ValidateURL(pageURL); err != nil {
			return err.Error()
		}
	}
	if profile != "" {
		if _, ok := h.catalog.Profiles[profile]; !ok {
			return fmt.Sprintf("Unknown profile %q", profile)
		}
	}
	if utf8.RuneCountInString(category) > 40 {
		return "Category must be at most 40 characters"
	}
	if logoURL != "" {
		if err := catalog.ValidateURL(logoURL); err != nil {
			return "Logo url " + err.Error()
		}
	}
	if len(aliases) > 20 {
		return "At most 20 aliases are allowed"
	}
	for _, alias := range aliases {
		if n := utf8.RuneCountInString(alias); n < 2 || n > 60 {
			return "Each alias must be between 2 and 60 characters"
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
