package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/store"
)

// These run against a real store and a real router, because the guards worth
// testing here live in the handlers: locking yourself out cannot be checked
// without them.
func testRouter(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set, skipping the integration test")
	}

	ctx := context.Background()
	st, err := store.New(ctx, dsn, store.Options{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Every request in these tests comes from the same address, so the per-IP
	// limits would trip part way through and make the result depend on how many
	// calls a test happens to make. They are the subject of their own tests.
	router := NewRouter(st, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Profiles:           catalog.Profiles{},
		RateLimitPerMinute: 60000,
		RateLimitBurst:     1000,
		SuggestPerHour:     36000,
		SuggestBurst:       1000,
		LoginPerHour:       36000,
		LoginBurst:         1000,
		PasswordPerHour:    36000,
		PasswordBurst:      1000,
	})
	return st, router
}

func testUser(t *testing.T, st *store.Store, role string) *store.User {
	t.Helper()

	email := fmt.Sprintf("zz-users-%d@example.com", time.Now().UnixNano())
	user, err := st.CreateUser(context.Background(), email, "Test Person", role, "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteUser(context.Background(), user.ID) })
	return user
}

func sessionFor(t *testing.T, st *store.Store, user *store.User) string {
	t.Helper()

	token, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := st.CreateSession(context.Background(), auth.HashToken(token), user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return "dr_session=" + token
}

func call(t *testing.T, router http.Handler, method, path, body, cookie string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAnAdminCannotLockThemselvesOut(t *testing.T) {
	st, router := testRouter(t)

	admin := testUser(t, st, auth.RoleAdmin)
	cookie := sessionFor(t, st, admin)
	path := fmt.Sprintf("/api/v1/users/%d", admin.ID)

	cases := []struct {
		label  string
		method string
		body   string
	}{
		{"demote myself", http.MethodPatch, `{"role":"moderator"}`},
		{"disable myself", http.MethodPatch, `{"disabled":true}`},
		{"delete myself", http.MethodDelete, ""},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			rec := call(t, router, tc.method, path, tc.body, cookie)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		})
	}

	// And the account is untouched, which is the point.
	after, err := st.UserByID(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if after.Role != auth.RoleAdmin || after.Disabled() {
		t.Errorf("the account was changed anyway: role=%q disabled=%v", after.Role, after.Disabled())
	}
}

func TestOnlyAnAdminMayManageUsers(t *testing.T) {
	st, router := testRouter(t)

	target := testUser(t, st, auth.RoleSubscriber)

	for _, role := range []string{auth.RoleModerator, auth.RoleSubscriber} {
		t.Run(role, func(t *testing.T) {
			cookie := sessionFor(t, st, testUser(t, st, role))

			if rec := call(t, router, http.MethodGet, "/api/v1/users", "", cookie); rec.Code != http.StatusForbidden {
				t.Errorf("list status = %d, want 403", rec.Code)
			}
			if rec := call(t, router, http.MethodPatch,
				fmt.Sprintf("/api/v1/users/%d", target.ID), `{"name":"Renamed"}`, cookie); rec.Code != http.StatusForbidden {
				t.Errorf("patch status = %d, want 403", rec.Code)
			}
			if rec := call(t, router, http.MethodDelete,
				fmt.Sprintf("/api/v1/users/%d", target.ID), "", cookie); rec.Code != http.StatusForbidden {
				t.Errorf("delete status = %d, want 403", rec.Code)
			}
		})
	}

	if rec := call(t, router, http.MethodGet, "/api/v1/users", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("list status without a session = %d, want 401", rec.Code)
	}
}

func TestAnAdminCanChangeAndDisableSomebodyElse(t *testing.T) {
	st, router := testRouter(t)

	admin := testUser(t, st, auth.RoleAdmin)
	cookie := sessionFor(t, st, admin)

	target := testUser(t, st, auth.RoleSubscriber)
	targetCookie := sessionFor(t, st, target)
	path := fmt.Sprintf("/api/v1/users/%d", target.ID)

	rec := call(t, router, http.MethodPatch, path,
		`{"name":"Renamed Person","email":"zz-renamed@example.com","role":"moderator"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	var patched struct {
		User struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if patched.User.Name != "Renamed Person" || patched.User.Role != "moderator" {
		t.Errorf("name/role = %q/%q", patched.User.Name, patched.User.Role)
	}

	// The address is free again once the account is gone, so this is only a
	// leftover to tidy up.
	t.Cleanup(func() {
		_, _ = st.UserByEmail(context.Background(), "zz-renamed@example.com")
	})

	// Disabling takes effect on the session they already had.
	if rec := call(t, router, http.MethodPatch, path, `{"disabled":true}`, cookie); rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if rec := call(t, router, http.MethodGet, "/api/v1/auth/me", "", targetCookie); !strings.Contains(rec.Body.String(), `"user":null`) {
		t.Errorf("a disabled account still resolves its session: %s", strings.TrimSpace(rec.Body.String()))
	}

	// And enabling it back is what makes disabling the gentler option.
	if rec := call(t, router, http.MethodPatch, path, `{"disabled":false}`, cookie); rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

func TestUserValidation(t *testing.T) {
	st, router := testRouter(t)

	admin := testUser(t, st, auth.RoleAdmin)
	cookie := sessionFor(t, st, admin)
	other := testUser(t, st, auth.RoleSubscriber)

	cases := []struct {
		label  string
		id     int64
		body   string
		status int
	}{
		{"nothing to update", other.ID, `{}`, http.StatusBadRequest},
		{"unknown role", other.ID, `{"role":"owner"}`, http.StatusBadRequest},
		{"name too short", other.ID, `{"name":"X"}`, http.StatusBadRequest},
		{"not an email address", other.ID, `{"email":"nope"}`, http.StatusBadRequest},
		{"email already used", other.ID, fmt.Sprintf(`{"email":%q}`, admin.Email), http.StatusConflict},
		{"unknown account", 999999999, `{"name":"Somebody"}`, http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			rec := call(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/users/%d", tc.id), tc.body, cookie)
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.status, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
}
