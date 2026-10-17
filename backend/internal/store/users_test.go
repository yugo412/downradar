package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func uniqueTestEmail() string {
	return fmt.Sprintf("zz-test-%d@example.com", time.Now().UnixNano())
}

func testTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func dropTestUser(ctx context.Context, st *Store, email string) {
	_, _ = st.pool.Exec(ctx, `delete from users where lower(email) = lower($1)`, email)
}

func TestCreateUserAndFindByEmail(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	created, err := st.CreateUser(ctx, email, "Test Person", "admin", "stored-hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.ID == 0 {
		t.Error("no id was assigned")
	}
	if created.Role != "admin" {
		t.Errorf("role = %q, want admin", created.Role)
	}

	found, err := st.UserByEmail(ctx, strings.ToUpper(email))
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("looking up an upper case address found id %d, want %d", found.ID, created.ID)
	}
	if found.PasswordHash != "stored-hash" {
		t.Errorf("hash = %q", found.PasswordHash)
	}
}

func TestCreateUserRejectsADuplicateEmail(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	if _, err := st.CreateUser(ctx, email, "First", "admin", "h"); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}

	_, err := st.CreateUser(ctx, strings.ToUpper(email), "Second", "moderator", "h")
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("err = %v, want ErrEmailTaken", err)
	}
}

func TestUserByEmailForAMissingAccount(t *testing.T) {
	st := integrationStore(t)

	if _, err := st.UserByEmail(context.Background(), uniqueTestEmail()); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSetUserPassword(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.CreateUser(ctx, email, "Test Person", "admin", "old-hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.SetUserPassword(ctx, user.ID, "new-hash"); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	found, err := st.UserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if found.PasswordHash != "new-hash" {
		t.Errorf("hash = %q, want new-hash", found.PasswordHash)
	}

	if err := st.SetUserPassword(ctx, 999999999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound for an unknown account", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.CreateUser(ctx, email, "Test Person", "moderator", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	tokenHash := testTokenHash("token-one")
	if err := st.CreateSession(ctx, tokenHash, user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	found, err := st.SessionUser(ctx, tokenHash)
	if err != nil {
		t.Fatalf("SessionUser: %v", err)
	}
	if found.ID != user.ID || found.Role != "moderator" {
		t.Errorf("session resolved to id %d role %q", found.ID, found.Role)
	}

	if err := st.DeleteSession(ctx, tokenHash); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := st.SessionUser(ctx, tokenHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound after signing out", err)
	}
}

func TestExpiredSessionIsNotResolved(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.CreateUser(ctx, email, "Test Person", "admin", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	expired := testTokenHash("expired-token")
	if err := st.CreateSession(ctx, expired, user.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.SessionUser(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want an expired session to be treated as missing", err)
	}

	removed, err := st.DeleteExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if removed == 0 {
		t.Error("the expired session was not cleaned up")
	}
}

func TestDeleteUserSessionsEndsEverySession(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.CreateUser(ctx, email, "Test Person", "admin", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for _, name := range []string{"one", "two"} {
		if err := st.CreateSession(ctx, testTokenHash(name), user.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("CreateSession %s: %v", name, err)
		}
	}

	removed, err := st.DeleteUserSessions(ctx, user.ID)
	if err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	if _, err := st.SessionUser(ctx, testTokenHash("one")); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSetUserName(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.CreateUser(ctx, email, "Old Name", "admin", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.SetUserName(ctx, user.ID, "New Name"); err != nil {
		t.Fatalf("SetUserName: %v", err)
	}
	found, err := st.UserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if found.Name != "New Name" {
		t.Errorf("name = %q, want New Name", found.Name)
	}
	if !found.UpdatedAt.After(found.CreatedAt) && !found.UpdatedAt.Equal(found.CreatedAt) {
		t.Errorf("updated_at %v went backwards from created_at %v", found.UpdatedAt, found.CreatedAt)
	}

	if err := st.SetUserName(ctx, 999999999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound for an unknown account", err)
	}
}

func TestDeleteOtherSessionsKeepsTheOneGiven(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.CreateUser(ctx, email, "Test Person", "admin", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	keep := testTokenHash("keep-me")
	for _, name := range []string{"keep-me", "phone", "laptop"} {
		if err := st.CreateSession(ctx, testTokenHash(name), user.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("CreateSession %s: %v", name, err)
		}
	}

	ended, err := st.DeleteOtherSessions(ctx, user.ID, keep)
	if err != nil {
		t.Fatalf("DeleteOtherSessions: %v", err)
	}
	if ended != 2 {
		t.Errorf("ended = %d, want 2", ended)
	}

	if _, err := st.SessionUser(ctx, keep); err != nil {
		t.Errorf("the session that asked for the change should survive: %v", err)
	}
	if _, err := st.SessionUser(ctx, testTokenHash("phone")); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want the other session to be gone", err)
	}
}

func TestListUsersCountsWhatEachAccountOwnsAndHolds(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	newPrivateService(t, st, owner)

	// An alert on a vendor the public can see is what the column counts, and one
	// that was unsubscribed stops counting: the same rule as the limit.
	id := alertService(t, st, uniqueName("user list alert"))
	token := uniqueName("token")
	if err := st.SubscribeAlert(ctx, id, &owner.ID, "owner@example.com", token); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	held := func(t *testing.T) UserView {
		t.Helper()

		users, err := st.ListUsers(ctx, "", "")
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		for _, u := range users {
			if u.ID == owner.ID {
				return u
			}
		}
		t.Fatal("the account is missing from the list")
		return UserView{}
	}

	u := held(t)
	if u.Services != 1 || u.Alerts != 1 {
		t.Errorf("services = %d and alerts = %d, want one of each", u.Services, u.Alerts)
	}
	if u.Disabled() {
		t.Error("a fresh account should not be disabled")
	}

	if err := st.UnsubscribeAlert(ctx, token); err != nil {
		t.Fatalf("UnsubscribeAlert: %v", err)
	}
	if u := held(t); u.Alerts != 0 {
		t.Errorf("alerts = %d after unsubscribing, want none", u.Alerts)
	}
}

func TestSetUserEmailAndRole(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	first := newTestUser(t, st, "subscriber")
	second := newTestUser(t, st, "subscriber")

	newEmail := "zz-renamed-" + uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, newEmail) })

	if err := st.SetUserEmail(ctx, first.ID, newEmail); err != nil {
		t.Fatalf("SetUserEmail: %v", err)
	}
	if err := st.SetUserRole(ctx, first.ID, "moderator"); err != nil {
		t.Fatalf("SetUserRole: %v", err)
	}

	found, err := st.UserByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if found.Email != newEmail || found.Role != "moderator" {
		t.Errorf("email/role = %q/%q", found.Email, found.Role)
	}

	// The unique index is on lower(email), so a taken address has to be refused
	// however it is capitalised.
	if err := st.SetUserEmail(ctx, second.ID, strings.ToUpper(newEmail)); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("err = %v, want ErrEmailTaken", err)
	}
}

func TestDisablingAnAccountEndsItsAccess(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "moderator")
	tokenHash := testTokenHash("live-session")
	if err := st.CreateSession(ctx, tokenHash, user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := st.SessionUser(ctx, tokenHash); err != nil {
		t.Fatalf("the session should work before disabling: %v", err)
	}

	if err := st.SetUserDisabled(ctx, user.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}
	if _, err := st.SessionUser(ctx, tokenHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want a disabled account's session to stop resolving", err)
	}

	// And back on again, which is the whole point of disabling over deleting.
	if err := st.SetUserDisabled(ctx, user.ID, false); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}
	if _, err := st.SessionUser(ctx, tokenHash); err != nil {
		t.Errorf("the session should resolve again after re-enabling: %v", err)
	}
}

func TestDeletingAnAccountTakesItsPrivateServices(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	slug := newPrivateService(t, st, owner)

	if err := st.DeleteUser(ctx, owner.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := st.UserByID(ctx, owner.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want the account gone", err)
	}
	if _, err := st.AdminGetService(ctx, slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want the private service to go with its owner", err)
	}
}

func TestCountEnabledAdmins(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	before, err := st.CountEnabledAdmins(ctx)
	if err != nil {
		t.Fatalf("CountEnabledAdmins: %v", err)
	}

	admin := newTestUser(t, st, "admin")
	after, err := st.CountEnabledAdmins(ctx)
	if err != nil {
		t.Fatalf("CountEnabledAdmins: %v", err)
	}
	if after != before+1 {
		t.Errorf("count went from %d to %d, want one more", before, after)
	}

	if err := st.SetUserDisabled(ctx, admin.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}
	disabled, err := st.CountEnabledAdmins(ctx)
	if err != nil {
		t.Fatalf("CountEnabledAdmins: %v", err)
	}
	if disabled != before {
		t.Errorf("a disabled admin was still counted: %d, want %d", disabled, before)
	}
}

func containsUser(list []UserView, id int64) bool {
	for _, u := range list {
		if u.ID == id {
			return true
		}
	}
	return false
}

func TestListUsersFiltersAndSearches(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	email := fmt.Sprintf("zz-search-%d@example.com", time.Now().UnixNano())
	user, err := st.CreateUser(ctx, email, "Zz Searchable Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	cases := []struct {
		label  string
		role   string
		search string
	}{
		{"by role", "subscriber", ""},
		{"by a fragment of the name", "", "searchable"},
		{"by name ignoring case", "", "SEARCHABLE"},
		{"by email", "", email},
		{"by a fragment of the email", "", "zz-search-"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got, err := st.ListUsers(ctx, tc.role, tc.search)
			if err != nil {
				t.Fatalf("ListUsers: %v", err)
			}
			if !containsUser(got, user.ID) {
				t.Errorf("ListUsers(%q, %q) did not return the account", tc.role, tc.search)
			}
		})
	}

	t.Run("the role filter leaves the others out", func(t *testing.T) {
		got, err := st.ListUsers(ctx, "admin", "")
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if containsUser(got, user.ID) {
			t.Error("a subscriber came back under the admin filter")
		}
		for _, u := range got {
			if u.Role != "admin" {
				t.Errorf("%s is not an admin but was returned", u.Email)
			}
		}
	})

	t.Run("a term that matches nothing", func(t *testing.T) {
		got, err := st.ListUsers(ctx, "", "zz-nothing-matches-this")
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d accounts, want none", len(got))
		}
	})

	t.Run("a percent sign is a literal, not a wildcard", func(t *testing.T) {
		all, err := st.ListUsers(ctx, "", "")
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		wildcard, err := st.ListUsers(ctx, "", "%")
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		if len(wildcard) >= len(all) {
			t.Errorf("searching %% returned %d of %d accounts, so it acted as a wildcard", len(wildcard), len(all))
		}
	})
}
