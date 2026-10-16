package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

// suggestionSlug posts a suggestion and returns the slug it was recorded under.
func suggestionSlug(t *testing.T, st *store.Store, router http.Handler, cookie string) string {
	t.Helper()

	name := fmt.Sprintf("zz-suggest-%d", time.Now().UnixNano())
	rec := call(t, router, http.MethodPost, "/api/v1/suggest", fmt.Sprintf(`{"name":%q}`, name), cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() { _, _ = st.DeleteService(context.Background(), body.Slug) })
	return body.Slug
}

// A signed in visitor who suggests a service is recorded as its owner, which is
// what the review queue shows. It is attribution, not a visibility choice.
func TestSuggestionRecordsTheSuggester(t *testing.T) {
	st, router := testRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	slug := suggestionSlug(t, st, router, cookie)

	row, err := st.AdminGetService(context.Background(), slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if row.OwnerID == nil || *row.OwnerID != subscriber.ID {
		t.Errorf("owner = %v, want %d", row.OwnerID, subscriber.ID)
	}
}

// A visitor without an account can still suggest, and the row is simply
// unowned.
func TestSuggestionFromAGuestHasNoOwner(t *testing.T) {
	st, router := testRouter(t)

	slug := suggestionSlug(t, st, router, "")

	row, err := st.AdminGetService(context.Background(), slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if row.OwnerID != nil {
		t.Errorf("owner = %v, want none", *row.OwnerID)
	}
}

// The service routes are the catalogue's own. A subscriber is kept out of all of
// them and pointed at the suggestion queue instead, so the refusal has to hold
// on every verb, not only on the list.
func TestServiceRoutesAreStaffOnly(t *testing.T) {
	st, router := testRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/services"},
		{http.MethodPost, "/api/v1/services"},
		{http.MethodGet, "/api/v1/services/some-slug"},
		{http.MethodPatch, "/api/v1/services/some-slug"},
		{http.MethodDelete, "/api/v1/services/some-slug"},
		{http.MethodGet, "/api/v1/services/some-slug/status"},
		{http.MethodGet, "/api/v1/services/some-slug/polls"},
		{http.MethodPost, "/api/v1/services/some-slug/detect"},
	}

	for _, route := range routes {
		rec := call(t, router, route.method, route.path, "", cookie)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a subscriber = %d, want 403: %s",
				route.method, route.path, rec.Code, rec.Body.String())
		}
	}
}

// The dashboard's own list is a read of the caller's rows, not a management
// screen, so a subscriber still gets it.
func TestOwnServicesAreOpenToAnySession(t *testing.T) {
	st, router := testRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	rec := call(t, router, http.MethodGet, "/api/v1/services/mine", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestServiceRoutesLetStaffIn(t *testing.T) {
	st, router := testRouter(t)

	moderator := testUser(t, st, auth.RoleModerator)
	cookie := sessionFor(t, st, moderator)

	rec := call(t, router, http.MethodGet, "/api/v1/services", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestServiceRoutesNeedASession(t *testing.T) {
	_, router := testRouter(t)

	rec := call(t, router, http.MethodGet, "/api/v1/services", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
