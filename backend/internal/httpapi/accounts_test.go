package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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
