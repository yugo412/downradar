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
