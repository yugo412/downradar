package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"downradar.info/backend/internal/auth"
)

// An account made through a provider has no password, so setting the first one
// must not ask for a current password it never had.
func TestAProviderAccountSetsItsFirstPassword(t *testing.T) {
	st, router := testRouter(t)
	ctx := context.Background()

	email := uniqueEmail()
	user, err := st.CreateUser(ctx, email, "Provider Person", "subscriber", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { dropUser(t, st, email) })

	cookie := sessionFor(t, st, user)
	rec := call(t, router, http.MethodPost, "/api/v1/auth/password",
		`{"currentPassword":"","newPassword":"a good password"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("setting the first password = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// It works as a password, so the account can now be reached both ways.
	rec = call(t, router, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"a good password"}`, "")
	if rec.Code != http.StatusOK {
		t.Errorf("sign in with the new password = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodGet, "/api/v1/auth/me", "", cookie)
	var body struct {
		User struct {
			HasPassword bool `json:"hasPassword"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.User.HasPassword {
		t.Error("the account says it has no password after one was set")
	}
}

// An account that has a password still has to give it: a session alone is not
// enough to replace one that somebody else may know.
func TestChangingAPasswordNeedsTheCurrentOne(t *testing.T) {
	st, router := testRouter(t)
	ctx := context.Background()

	hash, err := auth.HashPassword("the old password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	email := uniqueEmail()
	user, err := st.CreateUser(ctx, email, "Password Person", "subscriber", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { dropUser(t, st, email) })

	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/auth/password",
		`{"currentPassword":"not the old one","newPassword":"a good password"}`, cookie)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a wrong current password = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodPost, "/api/v1/auth/password",
		`{"currentPassword":"the old password","newPassword":"a good password"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Errorf("changing with the right password = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}
