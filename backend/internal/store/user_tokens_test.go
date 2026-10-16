package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRegisterUserIsASubscriberAndUnverified(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.RegisterUser(ctx, email, "New Person", "hash")
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	if user.Role != "subscriber" {
		t.Errorf("role = %q, want subscriber: the role is not the visitor's to choose", user.Role)
	}
	if user.Verified() {
		t.Error("an account registered from the site starts out verified")
	}

	// An account made from the shell is trusted: whoever ran the command can
	// already do anything on the server.
	handmade := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, handmade) })
	admin, err := st.CreateUser(ctx, handmade, "Made By Hand", "admin", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !admin.Verified() {
		t.Error("an account made from the shell starts out unverified")
	}
}

func TestUserTokensAreSingleUseAndExpire(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.RegisterUser(ctx, email, "New Person", "hash")
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	token := "zz-token-verify-" + email
	if err := st.CreateUserToken(ctx, user.ID, UserTokenVerify, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateUserToken: %v", err)
	}

	if got, err := st.UserByUserToken(ctx, UserTokenVerify, token); err != nil || got.ID != user.ID {
		t.Fatalf("UserByUserToken = %+v, %v", got, err)
	}
	// A confirmation link is not a password reset, and the other way round.
	if _, err := st.UserByUserToken(ctx, UserTokenReset, token); !errors.Is(err, ErrNotFound) {
		t.Errorf("a confirmation token was accepted as a reset: %v", err)
	}

	spent, err := st.SpendUserToken(ctx, UserTokenVerify, token)
	if err != nil {
		t.Fatalf("SpendUserToken: %v", err)
	}
	if !spent {
		t.Error("a live token was not spent")
	}
	if _, err := st.UserByUserToken(ctx, UserTokenVerify, token); !errors.Is(err, ErrNotFound) {
		t.Errorf("a spent token still resolves: %v", err)
	}
	if spent, err := st.SpendUserToken(ctx, UserTokenVerify, token); err != nil || spent {
		t.Errorf("spending it twice = %t, %v, want false", spent, err)
	}

	expired := "zz-token-expired-" + email
	if err := st.CreateUserToken(ctx, user.ID, UserTokenReset, expired, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("CreateUserToken: %v", err)
	}
	if _, err := st.UserByUserToken(ctx, UserTokenReset, expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("an expired token resolves: %v", err)
	}

	if _, err := st.DeleteExpiredUserTokens(ctx); err != nil {
		t.Fatalf("DeleteExpiredUserTokens: %v", err)
	}
	if _, err := st.UserByUserToken(ctx, UserTokenReset, expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("an expired token survived the cleanup: %v", err)
	}
}

func TestAskingForAnotherLinkRetiresTheOldOne(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.RegisterUser(ctx, email, "New Person", "hash")
	if err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	first := "zz-token-first-" + email
	second := "zz-token-second-" + email
	expiry := time.Now().Add(time.Hour)

	if err := st.CreateUserToken(ctx, user.ID, UserTokenVerify, first, expiry); err != nil {
		t.Fatalf("CreateUserToken: %v", err)
	}
	if err := st.CreateUserToken(ctx, user.ID, UserTokenVerify, second, expiry); err != nil {
		t.Fatalf("CreateUserToken: %v", err)
	}

	// Two live links of the same kind would mean two ways in from one inbox.
	if _, err := st.UserByUserToken(ctx, UserTokenVerify, first); !errors.Is(err, ErrNotFound) {
		t.Errorf("the older link still works: %v", err)
	}
	if _, err := st.UserByUserToken(ctx, UserTokenVerify, second); err != nil {
		t.Errorf("the newer link does not work: %v", err)
	}
}

func TestSetUserEmailTakesTheVerificationWithIt(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Made By Hand", "subscriber", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	moved := "zz-moved-" + email
	if err := st.SetUserEmail(ctx, user.ID, moved); err != nil {
		t.Fatalf("SetUserEmail: %v", err)
	}

	updated, err := st.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if updated.Verified() {
		t.Error("a new address is taken as confirmed")
	}
}
