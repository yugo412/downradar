package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/store"
)

// alertVendor writes a vendor the public can see, which is the only kind an
// alert can be subscribed to. The name is the slug, so a test can recognise the
// vendor in an answer that only carries its name.
func alertVendor(t *testing.T, st *store.Store) string {
	t.Helper()

	slug := fmt.Sprintf("zz-alert-vendor-%d", time.Now().UnixNano())
	created, err := st.CreateService(context.Background(), store.NewService{
		Name:                  slug,
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	if created != slug {
		t.Fatalf("slug = %q, want %q", created, slug)
	}
	t.Cleanup(func() {
		if _, err := st.DeleteService(context.Background(), slug); err != nil {
			t.Errorf("DeleteService: %v", err)
		}
	})
	return slug
}

func TestSubscribeAlertStoresTheAddress(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"Ann@Example.com"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	// Asking twice is not an error and says nothing about whether it was already
	// there: the answer must not become a way to test somebody else's address.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"ann@example.com"}`, "")
	if rec.Code != http.StatusCreated {
		t.Errorf("second status = %d, want 201", rec.Code)
	}

	// A broken session is not a reason to refuse a visitor their alert.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"ann@example.com"}`, "dr_session=not-a-real-token")
	if rec.Code != http.StatusCreated {
		t.Errorf("status with a stale session = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestSubscribeAlertRefusesWhatIsNotAnAddress(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	for _, body := range []string{`{}`, `{"email":""}`, `{"email":"not an address"}`, `{"email":"a@b"}`} {
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestSubscribeAlertOnlyKnowsPublicVendors(t *testing.T) {
	_, router := testRouter(t)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/zz-does-not-exist/alerts",
		`{"email":"ann@example.com"}`, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestSubscribeAlertIsRefusedWhenAlertsAreOff(t *testing.T) {
	st, _ := testRouter(t)
	slug := alertVendor(t, st)

	off := NewRouter(st, quietLogger(), Options{
		Catalog:             catalog.Catalog{},
		RateLimitPerMinute:  60000,
		RateLimitBurst:      1000,
		AlertPerHour:        36000,
		AlertBurst:          1000,
		SessionCookieSecure: false,
	})

	rec := call(t, off, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"ann@example.com"}`, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when there is no mail server", rec.Code)
	}
}

func TestSubscribeAlertGivesAGuestOneAlert(t *testing.T) {
	st, router := testRouter(t)
	first := alertVendor(t, st)
	second := alertVendor(t, st)

	post := func(slug, cookie string) (int, string) {
		t.Helper()
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
			`{"email":"Guest@Example.com"}`, cookie)
		return rec.Code, rec.Body.String()
	}

	if code, body := post(first, ""); code != http.StatusCreated {
		t.Fatalf("first alert status = %d, want 201: %s", code, body)
	}

	// Asking again for the same vendor is the same alert, not a second one.
	if code, body := post(first, ""); code != http.StatusCreated {
		t.Errorf("same vendor again = %d, want 201: %s", code, body)
	}

	// The refusal names the vendor the address is already subscribed to, so the
	// visitor knows which one to stop rather than having to guess.
	code, body := post(second, "")
	if code != http.StatusConflict {
		t.Fatalf("second vendor = %d, want 409 for a visitor with no account: %s", code, body)
	}
	if !strings.Contains(body, first) {
		t.Errorf("answer %q does not name %s", body, first)
	}

	// A subscriber may keep a few. The address they give does not have to be the
	// one on their account.
	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)
	if code, body := post(second, cookie); code != http.StatusCreated {
		t.Errorf("second vendor while signed in = %d, want 201: %s", code, body)
	}
	if code, body := post(first, cookie); code != http.StatusCreated {
		t.Errorf("the other vendor while signed in = %d, want 201: %s", code, body)
	}
}

func TestBellReadsAndStopsTheSignedInUsersAlert(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	// Nothing yet: the bell offers an alert.
	rec := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store for an answer about one session", got)
	}
	var state struct {
		Subscribed bool   `json:"subscribed"`
		Email      string `json:"email"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if state.Subscribed {
		t.Fatal("a person with no alert reads as subscribed")
	}

	// Subscribing while signed in is what the bell then has to see.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"work@example.org"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("subscribe status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !state.Subscribed {
		t.Fatal("the bell does not see the alert that was just made")
	}
	if state.Email != "w***@example.org" {
		t.Errorf("email = %q, want the masked address the alerts go to", state.Email)
	}

	// And the button in the modal stops it again.
	rec = call(t, router, http.MethodDelete, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("stop status = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if state.Subscribed {
		t.Error("the bell still sees an alert that was stopped")
	}
}

func TestAccountAlertsListAndAct(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"work@example.org"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("subscribe status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	type alertRow struct {
		ID           int64  `json:"id"`
		VendorSlug   string `json:"vendorSlug"`
		VendorStatus string `json:"vendorStatus"`
		Email        string `json:"email"`
		State        string `json:"state"`
	}
	list := func(t *testing.T) []alertRow {
		t.Helper()
		rec := call(t, router, http.MethodGet, "/api/v1/auth/alerts", "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("list status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Alerts []alertRow `json:"alerts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body.Alerts
	}

	alerts := list(t)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want the one that was just made", len(alerts))
	}
	// The account screen is the person's own, so the address is shown in full,
	// and each row carries the vendor's own status.
	if alerts[0].Email != "work@example.org" || alerts[0].State != "active" {
		t.Errorf("row = %+v, want the address and active", alerts[0])
	}
	if alerts[0].VendorStatus == "" {
		t.Errorf("row = %+v, want the vendor's status", alerts[0])
	}
	id := alerts[0].ID
	path := fmt.Sprintf("/api/v1/auth/alerts/%d", id)

	// Pause, then start it again.
	for _, state := range []string{"paused", "active"} {
		rec := call(t, router, http.MethodPatch, path, `{"state":"`+state+`"}`, cookie)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("set %s = %d, want 204: %s", state, rec.Code, rec.Body.String())
		}
		if got := list(t)[0].State; got != state {
			t.Errorf("state = %q, want %q", got, state)
		}
	}

	if rec := call(t, router, http.MethodPatch, path, `{"state":"whenever"}`, cookie); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown state = %d, want 400", rec.Code)
	}

	// Somebody else's row is not theirs to touch, and does not exist as far as
	// this session is concerned.
	other := testUser(t, st, auth.RoleSubscriber)
	otherCookie := sessionFor(t, st, other)
	if rec := call(t, router, http.MethodPatch, path, `{"state":"paused"}`, otherCookie); rec.Code != http.StatusNotFound {
		t.Errorf("pausing somebody else's alert = %d, want 404", rec.Code)
	}
	if rec := call(t, router, http.MethodDelete, path, "", otherCookie); rec.Code != http.StatusNotFound {
		t.Errorf("deleting somebody else's alert = %d, want 404", rec.Code)
	}

	if rec := call(t, router, http.MethodDelete, path, "", cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if alerts := list(t); len(alerts) != 0 {
		t.Errorf("alerts after deleting = %d, want none", len(alerts))
	}
}

func TestAccountAlertsNeedASession(t *testing.T) {
	_, router := testRouter(t)

	rec := call(t, router, http.MethodGet, "/api/v1/auth/alerts", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store for an answer about one session", got)
	}
}

func TestBellIsForSignedInPeopleOnly(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		rec := call(t, router, method, "/api/v1/vendors/"+slug+"/alerts", "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session = %d, want 401", method, rec.Code)
		}
	}
}

func TestUnsubscribeLinkResolvesAndThenWorks(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	token := "token-" + slug
	if err := st.SubscribeAlert(context.Background(), id, nil, "ann@example.com", token); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	rec := call(t, router, http.MethodGet, "/api/v1/alerts/"+token, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store for a page behind a token", got)
	}

	var view store.AlertView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.VendorSlug != slug || view.VendorName == "" {
		t.Errorf("view = %+v, want the vendor the token belongs to", view)
	}
	if view.UnsubscribedAt != nil {
		t.Error("a fresh subscription reads as unsubscribed")
	}

	rec = call(t, router, http.MethodDelete, "/api/v1/alerts/"+token, "", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe status = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// Pressing the button twice is not an error, and the link still says what it
	// is about.
	rec = call(t, router, http.MethodDelete, "/api/v1/alerts/"+token, "", "")
	if rec.Code != http.StatusNoContent {
		t.Errorf("second unsubscribe status = %d, want 204", rec.Code)
	}

	rec = call(t, router, http.MethodGet, "/api/v1/alerts/"+token, "", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.UnsubscribedAt == nil {
		t.Error("the page does not report the subscription as ended")
	}
}

func TestUnsubscribeLinkRefusesATokenThatIsNotOne(t *testing.T) {
	_, router := testRouter(t)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		rec := call(t, router, method, "/api/v1/alerts/not-a-token", "", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", method, rec.Code)
		}
	}
}

func TestParseEmail(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		problem string
	}{
		{"Ann@Example.com", "ann@example.com", ""},
		{"  spaced@example.com  ", "spaced@example.com", ""},
		{"", "", "required"},
		{"not an address", "", "does not look like"},
		{"a@b", "", "does not look like"},
		{"two@@example.com", "", "does not look like"},
		{strings.Repeat("a", 300) + "@example.com", "", "too long"},
	}

	for _, tc := range cases {
		got, problem := parseEmail(tc.in)
		if tc.problem == "" {
			if problem != "" || got != tc.want {
				t.Errorf("parseEmail(%q) = %q, %q, want %q", tc.in, got, problem, tc.want)
			}
			continue
		}
		if problem == "" {
			t.Errorf("parseEmail(%q) = %q, want it refused", tc.in, got)
		}
	}
}

// A subscriber may keep a few alerts, and the count is per account rather than
// per address: the address they give does not have to be the one on their
// account, so counting addresses would let the same person past the cap by
// typing a different one each time. Staff are not capped.
func TestSubscriberAlertsAreCapped(t *testing.T) {
	st, router := testRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	// The default cap, which the router applies when no option is given.
	const limit = 10

	slugs := make([]string, 0, limit+1)
	for i := 0; i <= limit; i++ {
		slugs = append(slugs, alertVendor(t, st))
	}

	subscribe := func(slug, cookie string) (int, string) {
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
			`{"email":"cap@example.org"}`, cookie)
		return rec.Code, rec.Body.String()
	}

	for _, slug := range slugs[:limit] {
		if code, body := subscribe(slug, cookie); code != http.StatusCreated {
			t.Fatalf("subscribe %s = %d, want 201: %s", slug, code, body)
		}
	}

	// The next vendor is one too many.
	if code, body := subscribe(slugs[limit], cookie); code != http.StatusConflict {
		t.Errorf("the alert past the limit = %d, want 409: %s", code, body)
	}

	// Asking again for one that is already there is not a new one, so it is not
	// refused: the count leaves the vendor being asked about out.
	if code, body := subscribe(slugs[0], cookie); code != http.StatusCreated {
		t.Errorf("re-subscribing = %d, want 201: %s", code, body)
	}

	// Removing one makes room again.
	rec := call(t, router, http.MethodDelete, "/api/v1/vendors/"+slugs[0]+"/alerts", "", cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if code, body := subscribe(slugs[limit], cookie); code != http.StatusCreated {
		t.Errorf("after removing one = %d, want 201: %s", code, body)
	}

	// A moderator may take the same set and more.
	moderator := testUser(t, st, auth.RoleModerator)
	modCookie := sessionFor(t, st, moderator)
	for _, slug := range slugs {
		if code, body := subscribe(slug, modCookie); code != http.StatusCreated {
			t.Fatalf("moderator subscribe %s = %d, want 201: %s", slug, code, body)
		}
	}
}

// The alerts screen is told how full the account is, so it can warn before the
// next alert is refused. Staff are not held to a count, so they are told
// nothing.
func TestAlertListReportsTheSubscriberLimit(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	read := func(t *testing.T, cookie string) (*int, int) {
		t.Helper()
		rec := call(t, router, http.MethodGet, "/api/v1/auth/alerts", "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Limit *int `json:"limit"`
			Used  int  `json:"used"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body.Limit, body.Used
	}

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	limit, used := read(t, cookie)
	if limit == nil || *limit != 10 {
		t.Errorf("limit = %v, want 10 for a subscriber", limit)
	}
	if used != 0 {
		t.Errorf("used = %d, want 0 before any alert", used)
	}

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"cap@example.org"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("subscribe = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if _, used := read(t, cookie); used != 1 {
		t.Errorf("used = %d, want 1 after one alert", used)
	}

	moderator := testUser(t, st, auth.RoleModerator)
	modCookie := sessionFor(t, st, moderator)
	if limit, _ := read(t, modCookie); limit != nil {
		t.Errorf("limit = %d, want none for staff", *limit)
	}
}

// The limit is read from the account's own row, so one subscriber can differ
// from another without touching the environment.
func TestSubscriberLimitComesFromTheAccountsOwnRow(t *testing.T) {
	st, router := testRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	if err := st.SetUserLimits(context.Background(), subscriber.ID, map[string]int{store.LimitAlert: 1}); err != nil {
		t.Fatalf("SetUserLimits: %v", err)
	}

	first := alertVendor(t, st)
	second := alertVendor(t, st)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+first+"/alerts",
		`{"email":"one@example.org"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+second+"/alerts",
		`{"email":"one@example.org"}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "1") {
		t.Errorf("answer %q does not name the limit", rec.Body.String())
	}

	// The alerts screen is told the same number, so it warns at the right point.
	rec = call(t, router, http.MethodGet, "/api/v1/auth/alerts", "", cookie)
	var body struct {
		Limit *int `json:"limit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Limit == nil || *body.Limit != 1 {
		t.Errorf("limit = %v, want 1 from the account's own row", body.Limit)
	}
}
