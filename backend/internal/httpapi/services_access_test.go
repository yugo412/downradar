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

// addService lets a session add a service and returns the slug it landed under.
func addService(t *testing.T, st *store.Store, router http.Handler, cookie, body string) string {
	t.Helper()

	rec := call(t, router, http.MethodPost, "/api/v1/services", body, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("adding a service = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var created struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() { _, _ = st.DeleteService(context.Background(), created.Slug) })
	return created.Slug
}

// A subscriber adds a service of their own. It is theirs whatever its
// visibility, no profile is taken from the body because the picker is not shown
// to them, and it is parked as no_match for the detection pass to look at.
func TestASubscriberAddsTheirOwnService(t *testing.T) {
	st, router := testRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	name := fmt.Sprintf("zz-own-%d", time.Now().UnixNano())
	slug := addService(t, st, router, cookie, fmt.Sprintf(
		`{"name":%q,"url":"https://status.example.invalid","profile":"statuspage-v2","visibility":"public"}`,
		name))

	row, err := st.AdminGetService(context.Background(), slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if row.OwnerID == nil || *row.OwnerID != subscriber.ID {
		t.Errorf("owner = %v, want %d", row.OwnerID, subscriber.ID)
	}
	if row.Profile != "" {
		t.Errorf("profile = %q, want none: the picker is not the subscriber's", row.Profile)
	}
	if row.Status != string(store.StatusNoMatch) {
		t.Errorf("status = %q, want no_match until a profile is found", row.Status)
	}

	// Their own row opens, which is the page they check it on.
	rec := call(t, router, http.MethodGet, "/api/v1/services/"+slug, "", cookie)
	if rec.Code != http.StatusOK {
		t.Errorf("the owner reading their own row = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// The list a subscriber opens is their own rows at every stage, and a service
// somebody else added is not there.
func TestASubscriberListIsOnlyTheirOwn(t *testing.T) {
	st, router := testRouter(t)

	owner := testUser(t, st, auth.RoleSubscriber)
	other := testUser(t, st, auth.RoleSubscriber)
	ownerCookie := sessionFor(t, st, owner)
	otherCookie := sessionFor(t, st, other)

	name := fmt.Sprintf("zz-mine-%d", time.Now().UnixNano())
	slug := addService(t, st, router, ownerCookie, fmt.Sprintf(
		`{"name":%q,"url":"https://status.example.invalid","visibility":"private"}`, name))

	mine := call(t, router, http.MethodGet, "/api/v1/services", "", ownerCookie)
	var mineBody struct {
		Services []struct {
			Slug string `json:"slug"`
		} `json:"services"`
	}
	if err := json.Unmarshal(mine.Body.Bytes(), &mineBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(mineBody.Services) != 1 || mineBody.Services[0].Slug != slug {
		t.Errorf("the owner's list = %+v, want only %s", mineBody.Services, slug)
	}

	theirs := call(t, router, http.MethodGet, "/api/v1/services", "", otherCookie)
	var theirBody struct {
		Services []struct {
			Slug string `json:"slug"`
		} `json:"services"`
	}
	if err := json.Unmarshal(theirs.Body.Bytes(), &theirBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(theirBody.Services) != 0 {
		t.Errorf("another subscriber's list = %+v, want nothing", theirBody.Services)
	}

	// And the row itself reads as missing, not as forbidden: a private service
	// is not confirmed to exist to anybody else.
	rec := call(t, router, http.MethodGet, "/api/v1/services/"+slug, "", otherCookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("reading somebody else's service = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// The queue that moves a service onto an adapter is staff's alone, and a row the
// caller cannot see is not there to patch either.
func TestASubscriberCannotReachTheStaffVerbs(t *testing.T) {
	st, router := testRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	routes := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/v1/services/some-slug/detect", http.StatusForbidden},
		{http.MethodPatch, "/api/v1/services/some-slug", http.StatusNotFound},
	}

	for _, route := range routes {
		rec := call(t, router, route.method, route.path, "", cookie)
		if rec.Code != route.want {
			t.Errorf("%s %s as a subscriber = %d, want %d: %s",
				route.method, route.path, rec.Code, route.want, rec.Body.String())
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

// A page the site already watches cannot be added a second time: the second row
// would never store a report, because one page is read by one vendor. The
// suggestion queue refuses this, and so does the add form.
func TestASubscriberCannotAddAServiceTwice(t *testing.T) {
	st, router := testRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	name := fmt.Sprintf("zz-twice-%d", time.Now().UnixNano())
	slug, err := st.CreateService(context.Background(), store.NewService{
		Name:   name,
		URL:    "https://status.example.invalid",
		Status: string(store.StatusNoMatch),
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { _, _ = st.DeleteService(context.Background(), slug) })

	rec := call(t, router, http.MethodPost, "/api/v1/services", fmt.Sprintf(
		`{"name":%q,"url":"https://status.example.invalid","visibility":"private"}`, name), cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("adding the same service twice = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// A service a subscriber published is in the catalogue, but its management shape
// carries the owner's address and the fetch log, so it stays theirs and the
// staff's. Everybody else reads the vendor through the public API.
func TestAPublishedServiceKeepsItsManagementShapeToItsOwner(t *testing.T) {
	st, router := testRouter(t)

	owner := testUser(t, st, auth.RoleSubscriber)
	other := testUser(t, st, auth.RoleSubscriber)
	otherCookie := sessionFor(t, st, other)

	name := fmt.Sprintf("zz-published-%d", time.Now().UnixNano())
	slug := addService(t, st, router, sessionFor(t, st, owner), fmt.Sprintf(
		`{"name":%q,"url":"https://status.example.invalid","visibility":"public"}`, name))

	row, err := st.AdminGetService(context.Background(), slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	// What the detection pass does once a profile fits.
	if err := st.SetDetectedProfile(context.Background(), row.ID, "statuspage-v2", 300, 60); err != nil {
		t.Fatalf("SetDetectedProfile: %v", err)
	}
	if _, err := st.GetVendor(context.Background(), slug, store.Public()); err != nil {
		t.Errorf("the catalogue cannot read a published service: %v", err)
	}

	rec := call(t, router, http.MethodGet, "/api/v1/services/"+slug, "", otherCookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("reading somebody else's published row = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// The owner edits the service they added, and only the parts of it that are
// theirs: the adapter, its endpoint and the stage stay where staff put them.
func TestASubscriberEditsTheirOwnService(t *testing.T) {
	st, router := testRouter(t)

	owner := testUser(t, st, auth.RoleSubscriber)
	other := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, owner)

	name := fmt.Sprintf("zz-edit-%d", time.Now().UnixNano())
	slug := addService(t, st, router, cookie, fmt.Sprintf(
		`{"name":%q,"url":"https://status.example.invalid","visibility":"private"}`, name))

	rec := call(t, router, http.MethodPatch, "/api/v1/services/"+slug, `{
		"name":"zz-renamed",
		"profile":"statuspage-v2",
		"status":"published",
		"active":false,
		"visibility":"public"
	}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("the owner editing their own service = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	row, err := st.AdminGetService(context.Background(), slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if row.Name != "zz-renamed" {
		t.Errorf("name = %q, want the one the owner sent", row.Name)
	}
	if row.Profile != "" {
		t.Errorf("profile = %q, want it left alone", row.Profile)
	}
	if row.Status != string(store.StatusNoMatch) {
		t.Errorf("status = %q, want the stage left alone", row.Status)
	}
	if !row.Active {
		t.Error("active = false, want the row left on the site")
	}
	if row.Visibility != store.VisibilityPublic {
		t.Errorf("visibility = %q, want the owner's own choice", row.Visibility)
	}

	// A page that moves is matched again, because the adapter on the row was
	// picked for the old one.
	if err := st.SetDetectedProfile(context.Background(), row.ID, "statuspage-v2", 300, 60); err != nil {
		t.Fatalf("SetDetectedProfile: %v", err)
	}
	rec = call(t, router, http.MethodPatch, "/api/v1/services/"+slug,
		`{"url":"https://moved.example.invalid"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("moving the page = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	row, err = st.AdminGetService(context.Background(), slug)
	if err != nil {
		t.Fatalf("AdminGetService: %v", err)
	}
	if row.Profile != "" || row.Status != string(store.StatusNoMatch) {
		t.Errorf("profile = %q, status = %q, want it parked for detection again", row.Profile, row.Status)
	}

	// Somebody else's row is not there for them to edit, not even as a 403.
	rec = call(t, router, http.MethodPatch, "/api/v1/services/"+slug,
		`{"name":"zz-hijacked"}`, sessionFor(t, st, other))
	if rec.Code != http.StatusNotFound {
		t.Errorf("editing somebody else's service = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
