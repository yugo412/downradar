package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

func uniqueEmail() string {
	return fmt.Sprintf("zz-accounts-%d@example.com", time.Now().UnixNano())
}

func dropUser(t *testing.T, st *store.Store, email string) {
	t.Helper()

	ctx := context.Background()
	user, err := st.UserByEmail(ctx, email)
	if err != nil {
		return
	}
	if err := st.DeleteUser(ctx, user.ID); err != nil {
		t.Errorf("DeleteUser: %v", err)
	}
}

func TestRegisterMakesASubscriberAndSendsAConfirmation(t *testing.T) {
	st, router, spy := testRouterWithMail(t)
	email := uniqueEmail()
	t.Cleanup(func() { dropUser(t, st, email) })

	rec := call(t, router, http.MethodPost, "/api/v1/auth/register",
		`{"name":"New Person","email":"`+email+`","password":"a good password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		User struct {
			Email      string `json:"email"`
			Role       string `json:"role"`
			VerifiedAt string `json:"verifiedAt"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.User.Role != "subscriber" {
		t.Errorf("role = %q, want subscriber", body.User.Role)
	}
	if body.User.VerifiedAt != "" {
		t.Errorf("verifiedAt = %q, want it unset until the link is followed", body.User.VerifiedAt)
	}

	// The answer sets a session, so registering is also signing in.
	if cookies := rec.Result().Cookies(); len(cookies) == 0 {
		t.Error("registering did not sign the person in")
	}

	sent := spy.sent()
	if len(sent) != 1 || sent[0].to != email {
		t.Fatalf("sent = %+v, want one confirmation to %s", sent, email)
	}
	if !strings.Contains(sent[0].body, "/verify?token=") {
		t.Fatalf("the confirmation has no link:\n%s", sent[0].body)
	}
	token := tokenFrom(t, sent[0].body)

	// And the link works: it is what proves the address.
	rec = call(t, router, http.MethodPost, "/api/v1/auth/verify", `{"token":"`+token+`"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Once only, which is the whole point of a single use link.
	rec = call(t, router, http.MethodPost, "/api/v1/auth/verify", `{"token":"`+token+`"}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("second verify status = %d, want 400", rec.Code)
	}
}

func TestRegisterRefusesOurOwnDomain(t *testing.T) {
	st, router, spy := testRouterWithMail(t)
	email := "someone@downradar.info"
	t.Cleanup(func() { dropUser(t, st, email) })

	rec := call(t, router, http.MethodPost, "/api/v1/auth/register",
		`{"name":"New Person","email":"`+email+`","password":"a good password"}`, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "could not be created") {
		t.Errorf("answer = %s, want the general one", rec.Body.String())
	}
	if len(spy.sent()) != 0 {
		t.Error("an email was sent for an account that was refused")
	}
	if _, err := st.UserByEmail(context.Background(), email); err == nil {
		t.Error("the account was created anyway")
	}
}

func TestRegisterRefusesATakenAddress(t *testing.T) {
	st, router, _ := testRouterWithMail(t)
	user := testUser(t, st, "subscriber")

	rec := call(t, router, http.MethodPost, "/api/v1/auth/register",
		`{"name":"New Person","email":"`+user.Email+`","password":"a good password"}`, "")
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestForgotAndReset(t *testing.T) {
	st, router, spy := testRouterWithMail(t)
	user := testUser(t, st, "subscriber")
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/auth/forgot",
		`{"email":"`+user.Email+`"}`, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("forgot status = %d, want 202: %s", rec.Code, rec.Body.String())
	}

	sent := spy.sent()
	if len(sent) != 1 {
		t.Fatalf("sent = %d messages, want one reset", len(sent))
	}
	if !strings.Contains(sent[0].body, "/reset?token=") {
		t.Fatalf("the reset has no link:\n%s", sent[0].body)
	}
	token := tokenFrom(t, sent[0].body)

	rec = call(t, router, http.MethodPost, "/api/v1/auth/reset",
		`{"token":"`+token+`","password":"a different password"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// A reset ends every session, including the one that asked for it.
	rec = call(t, router, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if !strings.Contains(rec.Body.String(), `"user":null`) {
		t.Errorf("me = %s, want the session gone", rec.Body.String())
	}

	// The new password works and the old one does not.
	for password, want := range map[string]int{
		"a different password": http.StatusOK,
		"hash":                 http.StatusUnauthorized,
	} {
		rec := call(t, router, http.MethodPost, "/api/v1/auth/login",
			`{"email":"`+user.Email+`","password":"`+password+`"}`, "")
		if rec.Code != want {
			t.Errorf("sign in with %q = %d, want %d", password, rec.Code, want)
		}
	}

	// And the link is spent.
	rec = call(t, router, http.MethodPost, "/api/v1/auth/reset",
		`{"token":"`+token+`","password":"yet another password"}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("second reset status = %d, want 400", rec.Code)
	}
}

func TestChangingTheAddressTakesTheConfirmationWithIt(t *testing.T) {
	st, router, spy := testRouterWithMail(t)
	user := testUser(t, st, "subscriber")
	cookie := sessionFor(t, st, user)

	moved := uniqueEmail()
	t.Cleanup(func() { dropUser(t, st, moved) })

	rec := call(t, router, http.MethodPatch, "/api/v1/auth/profile",
		`{"name":"Renamed Person","email":"`+moved+`"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		User struct {
			Email      string `json:"email"`
			Name       string `json:"name"`
			VerifiedAt string `json:"verifiedAt"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.User.Email != moved || body.User.Name != "Renamed Person" {
		t.Errorf("user = %+v, want the new address and name", body.User)
	}
	if body.User.VerifiedAt != "" {
		t.Error("a new address arrives already confirmed")
	}

	// And the new address gets its own link.
	sent := spy.sent()
	if len(sent) != 1 || sent[0].to != moved {
		t.Fatalf("sent = %+v, want one confirmation to %s", sent, moved)
	}
	if !strings.Contains(sent[0].body, "/verify?token=") {
		t.Errorf("the confirmation has no link:\n%s", sent[0].body)
	}

	// Our own domain is refused here too, and the address is left alone.
	rec = call(t, router, http.MethodPatch, "/api/v1/auth/profile",
		`{"name":"Renamed Person","email":"someone@downradar.info"}`, cookie)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	rec = call(t, router, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if !strings.Contains(rec.Body.String(), moved) {
		t.Errorf("me = %s, want the address unchanged", rec.Body.String())
	}
}

func TestForgotSaysNothingAboutUnknownAddresses(t *testing.T) {
	_, router, spy := testRouterWithMail(t)

	rec := call(t, router, http.MethodPost, "/api/v1/auth/forgot",
		`{"email":"nobody@example.com"}`, "")
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202 for an address that has no account", rec.Code)
	}
	if len(spy.sent()) != 0 {
		t.Error("an email was sent for an address that has no account")
	}
}

// The link in the inbox is a way in without a password, and following it proves
// the address it was sent to, so an account that was never confirmed by hand
// becomes confirmed on the way in.
func TestSignInWithALink(t *testing.T) {
	st, router, spy := testRouterWithMail(t)
	email := uniqueEmail()
	t.Cleanup(func() { dropUser(t, st, email) })

	rec := call(t, router, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Link Person","email":"`+email+`","password":"a good password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodPost, "/api/v1/auth/link", `{"email":"`+email+`"}`, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("link = %d, want 202: %s", rec.Code, rec.Body.String())
	}

	// Registering sent the confirmation; the sign in link is the message after.
	sent := spy.sent()
	if len(sent) != 2 {
		t.Fatalf("sent = %d messages, want a confirmation and a link", len(sent))
	}
	link := sent[len(sent)-1]
	if !strings.Contains(link.body, "/login?token=") {
		t.Fatalf("the message is not a sign in link:\n%s", link.body)
	}

	rec = call(t, router, http.MethodPost, "/api/v1/auth/link/session",
		`{"token":"`+tokenFrom(t, link.body)+`","remember":true}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sign in with the link = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		User struct {
			Email      string `json:"email"`
			VerifiedAt string `json:"verifiedAt"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.User.VerifiedAt == "" {
		t.Error("the link did not confirm the address it proved")
	}

	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "dr_session" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("the answer has no session in it")
	}
	if !cookie.Expires.After(time.Now()) {
		t.Errorf("cookie expires at %v, want it kept because remember me was set", cookie.Expires)
	}

	rec = call(t, router, http.MethodGet, "/api/v1/auth/me", "", "dr_session="+cookie.Value)
	if !strings.Contains(rec.Body.String(), email) {
		t.Errorf("me = %s, want the account the link belongs to", rec.Body.String())
	}

	// One link, one sign in.
	rec = call(t, router, http.MethodPost, "/api/v1/auth/link/session",
		`{"token":"`+tokenFrom(t, link.body)+`"}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("second use of the link = %d, want 400", rec.Code)
	}
}

// A link is a way in, not a way to get an account: an address nobody uses is
// refused and nothing is sent to it.
func TestASignInLinkNeedsAnAccount(t *testing.T) {
	_, router, spy := testRouterWithMail(t)
	email := uniqueEmail()

	rec := call(t, router, http.MethodPost, "/api/v1/auth/link", `{"email":"`+email+`"}`, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("link for an unknown address = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if sent := spy.sent(); len(sent) != 0 {
		t.Errorf("sent %d messages to an address with no account", len(sent))
	}
}

// The link carries the page to open once it has signed somebody in, as long as
// that page is on this site.
func TestASignInLinkKeepsAPageOnThisSite(t *testing.T) {
	st, router, spy := testRouterWithMail(t)
	user := testUser(t, st, "subscriber")

	rec := call(t, router, http.MethodPost, "/api/v1/auth/link",
		`{"email":"`+user.Email+`","next":"/alerts"}`, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("link = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if body := spy.sent()[0].body; !strings.Contains(body, "next=%2Falerts") {
		t.Errorf("the link does not carry the page to open:\n%s", body)
	}

	rec = call(t, router, http.MethodPost, "/api/v1/auth/link",
		`{"email":"`+user.Email+`","next":"https://elsewhere.invalid/"}`, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("link = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if body := spy.sent()[1].body; strings.Contains(body, "next=") {
		t.Errorf("the link carries an address off this site:\n%s", body)
	}
}

// Remember me decides whether the cookie outlives the browser: without it the
// cookie is one the browser keeps only while it is open.
func TestRememberMeDecidesWhetherTheCookieOutlivesTheBrowser(t *testing.T) {
	_, router, _ := testRouterWithMail(t)
	email := uniqueEmail()

	rec := call(t, router, http.MethodPost, "/api/v1/auth/register",
		`{"name":"Cookie Person","email":"`+email+`","password":"a good password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		label      string
		body       string
		persistent bool
	}{
		{"without remember me", `{"email":"` + email + `","password":"a good password"}`, false},
		{"with remember me", `{"email":"` + email + `","password":"a good password","remember":true}`, true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			rec := call(t, router, http.MethodPost, "/api/v1/auth/login", tc.body, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("sign in = %d, want 200: %s", rec.Code, rec.Body.String())
			}

			var cookie *http.Cookie
			for _, c := range rec.Result().Cookies() {
				if c.Name == "dr_session" {
					cookie = c
				}
			}
			if cookie == nil || cookie.Value == "" {
				t.Fatal("the answer has no session in it")
			}

			// A session cookie is one with no expiry for the browser to keep.
			if got := !cookie.Expires.IsZero() || cookie.MaxAge != 0; got != tc.persistent {
				t.Errorf("cookie expires = %v, max age = %d, want persistent = %v",
					cookie.Expires, cookie.MaxAge, tc.persistent)
			}
		})
	}
}

func testUserWithPassword(t *testing.T, st *store.Store, role, password string) *store.User {
	t.Helper()

	user := testUser(t, st, role)
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := st.SetUserPassword(context.Background(), user.ID, hash); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	return user
}

func TestASubscriberCanDeleteTheirAccount(t *testing.T) {
	st, router, spy := testRouterWithMail(t)
	ctx := context.Background()

	user := testUserWithPassword(t, st, auth.RoleSubscriber, "a good password")
	cookie := sessionFor(t, st, user)
	token := strings.TrimPrefix(cookie, "dr_session=")

	rec := call(t, router, http.MethodDelete, "/api/v1/auth/account",
		`{"password":"a good password"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d, want 200: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	if _, err := st.UserByID(ctx, user.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UserByID = %v, want the account gone", err)
	}
	if _, err := st.SessionUser(ctx, auth.HashToken(token)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SessionUser = %v, want the session gone with the account", err)
	}

	var cleared *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "dr_session" {
			cleared = c
		}
	}
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("the session cookie was not cleared: %+v", cleared)
	}

	sent := spy.sent()
	if len(sent) != 1 {
		t.Fatalf("sent = %d messages, want one farewell", len(sent))
	}
	farewell := sent[0]
	if farewell.to != user.Email {
		t.Errorf("the farewell went to %q, want %q", farewell.to, user.Email)
	}
	if !strings.Contains(strings.ToLower(farewell.subject), "deleted") {
		t.Errorf("subject = %q, want it to say the account is gone", farewell.subject)
	}
	for _, want := range []string{"cannot be undone", "cannot be restored"} {
		if !strings.Contains(farewell.body, want) {
			t.Errorf("the farewell does not contain %q:\n%s", want, farewell.body)
		}
	}
}

func TestDeletingAnAccountRefusesAWrongPassword(t *testing.T) {
	st, router := testRouter(t)
	ctx := context.Background()

	user := testUserWithPassword(t, st, auth.RoleSubscriber, "a good password")
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodDelete, "/api/v1/auth/account",
		`{"password":"not the password"}`, cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete = %d, want 403: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if _, err := st.UserByID(ctx, user.ID); err != nil {
		t.Errorf("the account was removed anyway: %v", err)
	}
}

func TestStaffCannotDeleteTheirOwnAccount(t *testing.T) {
	st, router := testRouter(t)

	for _, role := range []string{auth.RoleAdmin, auth.RoleModerator} {
		t.Run(role, func(t *testing.T) {
			user := testUserWithPassword(t, st, role, "a good password")
			cookie := sessionFor(t, st, user)

			rec := call(t, router, http.MethodDelete, "/api/v1/auth/account",
				`{"password":"a good password"}`, cookie)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("delete = %d, want 403: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
			}
			if _, err := st.UserByID(context.Background(), user.ID); err != nil {
				t.Errorf("the staff account was removed anyway: %v", err)
			}
		})
	}
}

func TestAProviderAccountSetsAPasswordBeforeDeleting(t *testing.T) {
	st, router := testRouter(t)
	ctx := context.Background()

	user := testUser(t, st, auth.RoleSubscriber)
	if err := st.SetUserPassword(ctx, user.ID, ""); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodDelete, "/api/v1/auth/account", `{"password":""}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete = %d, want 400: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if _, err := st.UserByID(ctx, user.ID); err != nil {
		t.Errorf("the account was removed anyway: %v", err)
	}
}

func TestDeletingAnAccountNeedsASession(t *testing.T) {
	_, router := testRouter(t)

	rec := call(t, router, http.MethodDelete, "/api/v1/auth/account",
		`{"password":"a good password"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("delete = %d, want 401: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}
