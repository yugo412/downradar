package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
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

// alertVendorWithComponents writes a vendor the public can see with a poll
// behind it, so it has components for the bell to list. The identifiers come
// back in the order the page shows them.
func alertVendorWithComponents(t *testing.T, st *store.Store, count int) (string, []string) {
	t.Helper()

	slug := alertVendor(t, st)
	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	components := make([]adapters.ComponentStatus, 0, count)
	sourceIDs := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		sourceIDs = append(sourceIDs, fmt.Sprintf("c%d", i))
		components = append(components, adapters.ComponentStatus{
			SourceID: fmt.Sprintf("c%d", i),
			Name:     fmt.Sprintf("Component %d", i),
			Position: i,
			Status:   adapters.HealthOK,
		})
	}

	if _, err := st.ApplyReport(context.Background(), id, adapters.Report{
		Global:     adapters.HealthOK,
		FetchedAt:  time.Now().UTC(),
		Components: components,
	}); err != nil {
		t.Fatalf("ApplyReport: %v", err)
	}
	return slug, sourceIDs
}

// bellState is what the modal reads before it draws its boxes.
type bellState struct {
	Subscribed    bool                          `json:"subscribed"`
	Email         string                        `json:"email"`
	Paused        bool                          `json:"paused"`
	EmailPaused   bool                          `json:"emailPaused"`
	WebhookPaused bool                          `json:"webhookPaused"`
	WholeService  bool                          `json:"wholeService"`
	Components    []string                      `json:"components"`
	Webhook       *store.AlertWebhook           `json:"webhook"`
	Lark          string                        `json:"lark"`
	LarkSecret    string                        `json:"larkSecret"`
	LarkPaused    bool                          `json:"larkPaused"`
	Calls         []store.WebhookCall           `json:"calls"`
	ChannelStatus []store.ChannelDeliveryStatus `json:"channelStatus"`
}

func readBell(t *testing.T, router http.Handler, slug, cookie string) bellState {
	t.Helper()

	rec := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("bell status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var state bellState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return state
}

// A Lark group is saved like the other subscriber ways: its webhook and signing
// secret come back masked on the owner's page, and its switch pauses only it.
func TestLarkWaySavesAndPauses(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	webhook := "https://open.larksuite.com/open-apis/bot/v2/hook/abc"
	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"lark":{"url":"`+webhook+`","secret":"a-secret"},"wholeService":true}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving a Lark way = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	state := readBell(t, router, slug, cookie)
	if state.Lark != webhook || state.LarkSecret != "a-secret" {
		t.Errorf("lark = %q, %q, want the saved webhook and secret", state.Lark, state.LarkSecret)
	}
	if state.LarkPaused {
		t.Error("a new Lark way is paused")
	}

	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/pause",
		`{"kind":"lark","paused":true}`, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pausing the Lark way = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if state = readBell(t, router, slug, cookie); !state.LarkPaused {
		t.Error("the Lark way did not pause")
	}
}

// A subscriber picks what the emails are about: the vendor's own status, or
// whichever of its components they ticked, or both. The bell reads the selection
// back, so it opens with the boxes they left ticked rather than starting over.
func TestSubscribeAlertPicksComponents(t *testing.T) {
	st, router := testRouter(t)
	slug, sourceIDs := alertVendorWithComponents(t, st, 2)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	subscribe := func(body string) (int, string) {
		t.Helper()
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie)
		return rec.Code, rec.Body.String()
	}

	// One component, and not the vendor's own status.
	body := fmt.Sprintf(`{"email":"parts@example.org","wholeService":false,"components":["%s"]}`,
		sourceIDs[0])
	if code, out := subscribe(body); code != http.StatusCreated {
		t.Fatalf("picking a component = %d, want 201: %s", code, out)
	}

	state := readBell(t, router, slug, cookie)
	if !state.Subscribed || state.WholeService {
		t.Errorf("bell = %+v, want a subscription to the component alone", state)
	}
	if len(state.Components) != 1 || state.Components[0] != sourceIDs[0] {
		t.Errorf("components = %v, want %s", state.Components, sourceIDs[0])
	}

	// The account screen says what each row watches, so a row is more than a
	// vendor name.
	rec := call(t, router, http.MethodGet, "/api/v1/auth/alerts", "", cookie)
	var listed struct {
		Alerts []struct {
			WholeService bool     `json:"wholeService"`
			Components   []string `json:"components"`
			VendorName   string   `json:"vendorName"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Alerts) != 1 || listed.Alerts[0].WholeService {
		t.Fatalf("rows = %+v, want the one component subscription", listed.Alerts)
	}
	if len(listed.Alerts[0].Components) != 1 || listed.Alerts[0].Components[0] != "Component 1" {
		t.Errorf("row components = %v, want the component by name", listed.Alerts[0].Components)
	}

	// Both can be watched at once, which is what the two kinds of tick are for.
	body = fmt.Sprintf(`{"email":"parts@example.org","wholeService":true,"components":["%s","%s"]}`,
		sourceIDs[0], sourceIDs[1])
	if code, out := subscribe(body); code != http.StatusCreated {
		t.Fatalf("picking the vendor and two components = %d, want 201: %s", code, out)
	}
	state = readBell(t, router, slug, cookie)
	if !state.WholeService || len(state.Components) != 2 {
		t.Errorf("bell = %+v, want the vendor's own status and both components", state)
	}

	// A request in the older shape, naming nothing, still means the vendor's own
	// status: only the bell sends this, and a page from before this change does.
	if code, out := subscribe(`{"email":"parts@example.org"}`); code != http.StatusCreated {
		t.Fatalf("the older shape = %d, want 201: %s", code, out)
	}
	state = readBell(t, router, slug, cookie)
	if !state.WholeService || len(state.Components) != 0 {
		t.Errorf("bell = %+v, want only the vendor's own status", state)
	}

	// A component that is not this vendor's is refused rather than quietly
	// dropped: what is written has to be what the subscriber was shown.
	body = fmt.Sprintf(`{"email":"parts@example.org","wholeService":false,"components":["%s","nope"]}`,
		sourceIDs[0])
	if code, out := subscribe(body); code != http.StatusBadRequest {
		t.Errorf("an unknown component = %d, want 400: %s", code, out)
	}
}

// A visitor without an account gets one alert, and it is a whole vendor:
// components are a subscriber's, so naming one is what signing in is for.
func TestAGuestWatchesWholeVendorsOnly(t *testing.T) {
	st, router := testRouter(t)
	slug, sourceIDs := alertVendorWithComponents(t, st, 2)

	post := func(body string) (int, string) {
		t.Helper()
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, "")
		return rec.Code, rec.Body.String()
	}

	body := fmt.Sprintf(`{"email":"guest@example.org","wholeService":false,"components":["%s"]}`,
		sourceIDs[0])
	if code, out := post(body); code != http.StatusConflict {
		t.Errorf("a component for a guest = %d, want 409: %s", code, out)
	}

	// Ticking the vendor as well does not make a component available, because
	// that would be two things to send mail about.
	body = fmt.Sprintf(`{"email":"guest@example.org","wholeService":true,"components":["%s"]}`,
		sourceIDs[0])
	if code, out := post(body); code != http.StatusConflict {
		t.Errorf("a component beside the vendor for a guest = %d, want 409: %s", code, out)
	}

	if code, out := post(`{"email":"guest@example.org"}`); code != http.StatusCreated {
		t.Errorf("the vendor for a guest = %d, want 201: %s", code, out)
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
	// The address is read back in full: the page that edits it shows one address,
	// and this is the account's own answer. Only the link that can be forwarded,
	// which the unsubscribe page opens, masks it.
	if state.Email != "work@example.org" {
		t.Errorf("email = %q, want the address the alert goes to", state.Email)
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
	if err := st.SubscribeAlert(context.Background(), id, nil, store.EmailOnly("ann@example.com", token, store.WholeVendor)); err != nil {
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

// A subscriber may keep a few alerts, and the count is per account rather than per
// address: the address they give need not be their account's, so counting addresses
// would let the same person past the cap by typing a new one. Staff are not capped.
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

	// Asking again for what is already there is not a new thing to watch, so it
	// is not refused: what the address already watches on this vendor is what the
	// request replaces.
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

// The cap counts vendors rather than the things watched inside one, so a whole
// status page and several of its components is a single place in it, and a vendor
// picked only through a component is a vendor all the same.
func TestSubscriberAlertsAreCappedPerVendor(t *testing.T) {
	st, router := testRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	const limit = 3
	if err := st.SetUserLimits(context.Background(), subscriber.ID,
		map[string]int{store.LimitAlert: limit}); err != nil {
		t.Fatalf("SetUserLimits: %v", err)
	}

	used := func() int {
		t.Helper()
		rec := call(t, router, http.MethodGet, "/api/v1/auth/alerts", "", cookie)
		var body struct {
			Used int `json:"used"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body.Used
	}

	slug, sourceIDs := alertVendorWithComponents(t, st, limit+1)

	quoted := make([]string, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		quoted = append(quoted, fmt.Sprintf("%q", id))
	}
	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"parts@example.org","wholeService":true,"components":[`+
			strings.Join(quoted, ",")+`]}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a vendor with its components = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if got := used(); got != 1 {
		t.Errorf("used = %d after one vendor, want 1", got)
	}

	// Every vendor after that takes a place, and nothing is left by the time the
	// count is full.
	for i := 1; i < limit; i++ {
		other := alertVendor(t, st)
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+other+"/alerts",
			`{"email":"parts@example.org"}`, cookie)
		if rec.Code != http.StatusCreated {
			t.Fatalf("vendor %d = %d, want 201: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if got := used(); got != limit {
		t.Errorf("used = %d, want %d", got, limit)
	}

	// A vendor watched only through one of its components is still a vendor.
	other, ids := alertVendorWithComponents(t, st, 1)
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+other+"/alerts",
		`{"email":"parts@example.org","wholeService":false,"components":[`+
			fmt.Sprintf("%q", ids[0])+`]}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Errorf("a component past the limit = %d, want 409: %s", rec.Code, rec.Body.String())
	}

	// Asking again for a vendor that is already watched replaces what is there,
	// so it fits without a place of its own.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"parts@example.org","wholeService":false,"components":[`+
			fmt.Sprintf("%q", sourceIDs[0])+`]}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Errorf("changing mind on a watched vendor = %d, want 201: %s", rec.Code, rec.Body.String())
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

// A webhook is the second way one alert can arrive, and it belongs to an account:
// there is nowhere to keep one for a visitor who has none. A save writes the ways
// it names and leaves the rest alone, so the page can hold both.
func TestAWebhookIsPartOfTheSignedInUsersAlert(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	callHook := `{"url":"https://hooks.example.invalid/downradar","method":"PUT",` +
		`"headers":[{"name":"X-Source","value":"downradar"}]}`

	// A visitor with no account cannot leave a webhook anywhere.
	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"guest@example.org","webhook":`+callHook+`}`, "")
	if rec.Code != http.StatusConflict {
		t.Errorf("a webhook for a guest = %d, want 409: %s", rec.Code, rec.Body.String())
	}

	body := `{"email":"hooks@example.org","wholeService":true,"webhook":` + callHook + `}`
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving both ways = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	state := readBell(t, router, slug, cookie)
	if !state.Subscribed || state.Email != "hooks@example.org" {
		t.Errorf("bell = %+v, want the address it was saved with", state)
	}
	if state.Webhook == nil {
		t.Fatal("the page cannot read back a webhook it just wrote")
	}
	if state.Webhook.URL != "https://hooks.example.invalid/downradar" ||
		state.Webhook.Method != "PUT" {
		t.Errorf("webhook = %+v, want the call it was saved with", state.Webhook)
	}
	if len(state.Webhook.Headers) != 1 {
		t.Fatalf("headers = %+v, want only the one that was written", state.Webhook.Headers)
	}
	if got := state.Webhook.Headers[0]; got.Name != "X-Source" || got.Value != "downradar" {
		t.Errorf("the subscriber's header = %+v, want it kept as it was written", got)
	}

	// Saving the same thing again leaves the header list where it was rather
	// than growing it with something the subscriber did not write.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving again = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	again := readBell(t, router, slug, cookie)
	if again.Webhook == nil || len(again.Webhook.Headers) != 1 ||
		again.Webhook.Headers[0].Value != "downradar" {
		t.Errorf("headers = %+v, want the one that was written and nothing more", again.Webhook.Headers)
	}

	// Clearing the URL stops the calls and leaves the emails alone.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"hooks@example.org","wholeService":true,"webhook":{"url":""}}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("clearing the webhook = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	stopped := readBell(t, router, slug, cookie)
	if stopped.Webhook != nil {
		t.Errorf("webhook = %+v, want the calls stopped", stopped.Webhook)
	}
	if stopped.Email != "hooks@example.org" {
		t.Errorf("email = %q, want the emails left alone", stopped.Email)
	}

	// The bell says nothing about a webhook, so an address-only save from it does
	// not stop one that is there.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"hooks@example.org"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the bell's shape = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

// A webhook is checked before it is kept: the sender could not make a call out of
// any of these, and a receiver that is called at all should be called properly.
func TestAWebhookIsRefusedWhenItCouldNotBeSent(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	for _, webhook := range []string{
		`{"url":"not a url"}`,
		`{"url":"/relative"}`,
		`{"url":"ftp://hooks.example.invalid/x"}`,
		`{"url":"https://hooks.example.invalid/x","method":"DELETE"}`,
		// A GET has nowhere to put what a call carries.
		`{"url":"https://hooks.example.invalid/x","method":"GET"}`,
		`{"url":"https://hooks.example.invalid/x","headers":[{"name":"","value":"x"}]}`,
		`{"url":"https://hooks.example.invalid/x","headers":[{"name":"X-Bad","value":"one\r\ntwo"}]}`,
	} {
		body := `{"email":"hooks@example.org","webhook":` + webhook + `}`
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", webhook, rec.Code, rec.Body.String())
		}
	}

	// A webhook with no headers is the plain call, which is what somebody who
	// only wants to know that something moved needs.
	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"hooks@example.org","webhook":{"url":"https://hooks.example.invalid/ping"}}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Errorf("a webhook with no headers = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

// A webhook-only alert takes a place at the cap like any other, and saving over
// the alert an account already has on this vendor takes none: adding a webhook
// beside an address, or moving one, is being replaced rather than added.
func TestAWebhookTakesAPlaceAtTheCap(t *testing.T) {
	st, _ := testRouter(t)
	first := alertVendor(t, st)
	second := alertVendor(t, st)
	third := alertVendor(t, st)

	router := NewRouter(st, quietLogger(), Options{
		Catalog:                 catalog.Catalog{},
		RateLimitPerMinute:      60000,
		RateLimitBurst:          1000,
		AlertPerHour:            36000,
		AlertBurst:              1000,
		AlertLimitPerSubscriber: 2,
		SessionCookieSecure:     false,
		AlertsEnabled:           true,
	})

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	post := func(slug, body string) *httptest.ResponseRecorder {
		t.Helper()
		return call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie)
	}

	// Two vendors fills the cap.
	for _, slug := range []string{first, second} {
		if rec := post(slug, `{"email":"cap@example.org"}`); rec.Code != http.StatusCreated {
			t.Fatalf("filling the cap = %d, want 201: %s", rec.Code, rec.Body.String())
		}
	}

	// A webhook beside the alert on the first vendor is the same place, not
	// another one, even though it arrives a second way.
	rec := post(first, `{"email":"cap@example.org","webhook":{"url":"https://hooks.example.invalid/a"}}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("a webhook over an alert that is already there = %d, want 201: %s",
			rec.Code, rec.Body.String())
	}

	// A third vendor is a third place, whether it arrives by address or by
	// webhook alone.
	if rec := post(third, `{"email":"cap@example.org"}`); rec.Code != http.StatusConflict {
		t.Errorf("a third vendor = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if rec := post(third, `{"webhook":{"url":"https://hooks.example.invalid/c"}}`); rec.Code != http.StatusConflict {
		t.Errorf("a third vendor by webhook = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// An address with nothing picked beside it would be told nothing, and an email
// reports on what was asked for, so a save with no component and no whole vendor is
// refused. A webhook needs no pick, because it always carries the vendor itself.
func TestAnEmailWithNothingToReportOnIsRefused(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	for _, body := range []string{
		`{"email":"nothing@example.org","wholeService":false,"components":[]}`,
		`{"email":"nothing@example.org","wholeService":false,"components":[],` +
			`"webhook":{"url":"https://hooks.example.invalid/nothing"}}`,
	} {
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
	}

	// Without an address there is nothing to report on, so a webhook on its own
	// is the whole alert.
	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"wholeService":false,"components":[],"webhook":{"url":"https://hooks.example.invalid/only"}}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Errorf("a webhook on its own = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

// hookServer is an endpoint a test can point a webhook at, so a try has somewhere
// real to land. It answers with what it was told to, in every sense.
func hookServer(t *testing.T, reply int, body string) (*httptest.Server, func() string) {
	t.Helper()

	var mu sync.Mutex
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = string(raw)
		mu.Unlock()
		w.WriteHeader(reply)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return server, func() string {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

// The page can try the webhook it is holding, saved or not, and is shown what the
// far end said. It is the same call a delivery would make, with a change that never
// happened, and it is written down like one so the page can show what it did.
func TestTryingAWebhookFromThePage(t *testing.T) {
	st, router := testRouter(t)
	ctx := context.Background()
	slug, _ := alertVendorWithComponents(t, st, 1)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	server, lastBody := hookServer(t, http.StatusAccepted, `{"queued":true}`)
	try := `{"url":"` + server.URL + `/hook","method":"PATCH",` +
		`"headers":[{"name":"X-Source","value":"downradar"},{"name":"X-What","value":"{{component}}"}]}`

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/test", try, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("a try = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var answer struct {
		Status   *int   `json:"status"`
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Status == nil || *answer.Status != http.StatusAccepted {
		t.Errorf("status = %v, want what the far end answered", answer.Status)
	}
	if answer.Response != `{"queued":true}` {
		t.Errorf("response = %q, want the body it answered with", answer.Response)
	}
	if answer.Error != "" {
		t.Errorf("error = %q, want none for a call that was answered", answer.Error)
	}

	// What the far end received is the call the page described, with the vendor's
	// own parts in it.
	var payload struct {
		Service struct {
			Slug   string `json:"slug"`
			Status string `json:"status"`
		} `json:"service"`
		Changed struct {
			Component string `json:"component"`
			From      string `json:"from"`
			To        string `json:"to"`
		} `json:"changed"`
		Components []struct {
			Name string `json:"name"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(lastBody()), &payload); err != nil {
		t.Fatalf("the far end was called with %q, want JSON: %v", lastBody(), err)
	}
	if payload.Service.Slug != slug || payload.Service.Status != "ok" {
		t.Errorf("service = %+v, want the vendor the page is about", payload.Service)
	}
	if payload.Changed.Component != "Component 1" {
		t.Errorf("changed.component = %q, want the vendor's own component", payload.Changed.Component)
	}
	if payload.Changed.From != "ok" || payload.Changed.To != "major" {
		t.Errorf("changed = %q to %q, want the example change", payload.Changed.From, payload.Changed.To)
	}
	if len(payload.Components) != 1 {
		t.Errorf("components = %+v, want the vendor's own list", payload.Components)
	}

	// Nothing was saved, so the bell still says there is no alert here, while the
	// try is kept where the page can read it back.
	if state := readBell(t, router, slug, cookie); state.Subscribed {
		t.Error("a try wrote an alert")
	}
	serviceID, _, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	logged, err := st.WebhookCalls(ctx, subscriber.ID, serviceID, 10)
	if err != nil {
		t.Fatalf("WebhookCalls: %v", err)
	}
	if len(logged) != 1 || logged[0].Kind != store.CallTest {
		t.Fatalf("calls written down = %+v, want the try that was made", logged)
	}
	if logged[0].Status == nil || *logged[0].Status != http.StatusAccepted {
		t.Errorf("the try was written down as %v, want what it answered", logged[0].Status)
	}
	if state := readBell(t, router, slug, cookie); len(state.Calls) != 1 {
		t.Errorf("the page reads back %d calls, want the one that was made", len(state.Calls))
	}

	// A far end that refuses is an answer the page shows rather than a mistake of
	// this endpoint's own.
	refusing, _ := hookServer(t, http.StatusBadGateway, "no")
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/test",
		`{"url":"`+refusing.URL+`"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("a refused try = %d, want the answer shown back: %s", rec.Code, rec.Body.String())
	}
	answer = struct {
		Status   *int   `json:"status"`
		Response string `json:"response"`
		Error    string `json:"error"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Status == nil || *answer.Status != http.StatusBadGateway || answer.Error == "" {
		t.Errorf("answer = %+v, want the status and the line the far end gave", answer)
	}

	// What the page is holding has to be a webhook before anything is called.
	for _, bad := range []string{`{"url":""}`, `{"url":"not a url"}`, `{"url":"https://x.example/","method":"GET"}`} {
		rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/test", bad, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", bad, rec.Code, rec.Body.String())
		}
	}

	// A webhook hangs off an account, so a visitor with no session has nothing to
	// try it with, and a vendor the public cannot see has nothing to try.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/test",
		`{"url":"`+server.URL+`"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a try with no session = %d, want 401", rec.Code)
	}
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/zz-not-a-vendor/alerts/test",
		`{"url":"`+server.URL+`"}`, cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("a try on a vendor that is not there = %d, want 404", rec.Code)
	}
}

// Trying the setup is not the same act as subscribing, and a few corrections in a
// row are expected. The test budget is therefore its own, even when the much
// smaller subscription budget is already spent.
func TestTryingAWebhookHasItsOwnBudget(t *testing.T) {
	st, router, _ := testRouterWithOptions(t, func(opts *Options) {
		opts.AlertBurst = 1
		opts.AlertTestBurst = 3
	})
	slug, _ := alertVendorWithComponents(t, st, 1)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	server, _ := hookServer(t, http.StatusOK, "ok")
	try := `{"url":"` + server.URL + `"}`

	for i := 0; i < 3; i++ {
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/test", try, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("try %d = %d, want 200: %s", i+1, rec.Code, rec.Body.String())
		}
	}
}

// Either way of an alert can be made quiet on its own without taking away what it
// was told; stopping a way for good is clearing its field and saving. Both quiet is
// a paused alert on the account screen, whose button starts every way at once.
func TestPausingOneWayFromThePage(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"email":"pause@example.org","wholeService":true,`+
			`"webhook":{"url":"https://hooks.example.invalid/pause","method":"POST"}}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving both ways = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/pause",
		`{"kind":"email","paused":true}`, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pausing the emails = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	state := readBell(t, router, slug, cookie)
	if !state.EmailPaused || state.WebhookPaused {
		t.Errorf("ways are quiet = email %v, webhook %v, want only the email",
			state.EmailPaused, state.WebhookPaused)
	}
	if state.Paused {
		t.Error("one quiet way made the whole alert paused")
	}
	if state.Email != "pause@example.org" || state.Webhook == nil {
		t.Error("pausing a way took away what it was told")
	}

	// Both quiet is the state the account screen acts on, and its Resume starts
	// everything again rather than only the row it was pressed on.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/pause",
		`{"kind":"webhook","paused":true}`, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pausing the calls = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if state := readBell(t, router, slug, cookie); !state.Paused {
		t.Error("every way quiet did not read back as a paused alert")
	}

	var list struct {
		Alerts []struct {
			ID         int64  `json:"id"`
			VendorSlug string `json:"vendorSlug"`
			State      string `json:"state"`
		} `json:"alerts"`
	}
	rec = call(t, router, http.MethodGet, "/api/v1/auth/alerts", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Alerts) != 1 || list.Alerts[0].VendorSlug != slug || list.Alerts[0].State != "paused" {
		t.Fatalf("alerts = %+v, want this one shown as paused", list.Alerts)
	}

	rec = call(t, router, http.MethodPatch,
		fmt.Sprintf("/api/v1/auth/alerts/%d", list.Alerts[0].ID), `{"state":"active"}`, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resuming = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	state = readBell(t, router, slug, cookie)
	if state.Paused || state.EmailPaused || state.WebhookPaused {
		t.Errorf("resuming left something quiet: %+v", state)
	}

	// A way the alert does not have cannot be paused, and neither can a kind this
	// site does not send.
	other := alertVendor(t, st)
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+other+"/alerts/pause",
		`{"kind":"email","paused":true}`, cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("pausing a way that is not there = %d, want 404", rec.Code)
	}
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/pause",
		`{"kind":"sms","paused":true}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("pausing a kind that is not sent = %d, want 400", rec.Code)
	}

	// It is the account's own alert, so it needs the session.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/pause",
		`{"kind":"email","paused":true}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("pausing with no session = %d, want 401", rec.Code)
	}
}
