package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func uniqueSubject() string {
	return fmt.Sprintf("zz-subject-%d", time.Now().UnixNano())
}

func TestAProviderAccountIsMadeVerifiedAndLinked(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	email := uniqueTestEmail()
	subject := uniqueSubject()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.CreateProviderUser(ctx, "zz-provider", subject, email,
		"Provider Person", email, "provider-person")
	if err != nil {
		t.Fatalf("CreateProviderUser: %v", err)
	}
	if user.Role != "subscriber" {
		t.Errorf("role = %q, want subscriber", user.Role)
	}
	if user.VerifiedAt == nil {
		t.Error("the provider proved the address, so the account should be confirmed")
	}
	if user.PasswordHash != "" {
		t.Errorf("password hash = %q, want none for a provider account", user.PasswordHash)
	}

	// The provider's own id finds the account later, without the address.
	found, err := st.UserByProvider(ctx, "zz-provider", subject)
	if err != nil {
		t.Fatalf("UserByProvider: %v", err)
	}
	if found.ID != user.ID {
		t.Errorf("id = %d, want %d", found.ID, user.ID)
	}

	// The same address cannot become a second account.
	if _, err := st.CreateProviderUser(ctx, "zz-provider", uniqueSubject(), email,
		"Second Person", email, "second"); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("second account on the same address = %v, want ErrEmailTaken", err)
	}

	// Nor can an identity that already belongs to somebody be linked elsewhere.
	other := testStoreUser(t, st)
	if err := st.LinkProvider(ctx, other.ID, "zz-provider", subject, email, "provider-person"); !errors.Is(err, ErrProviderTaken) {
		t.Errorf("linking a taken identity = %v, want ErrProviderTaken", err)
	}

	// A fresh identity links to an account that already exists.
	fresh := uniqueSubject()
	if err := st.LinkProvider(ctx, other.ID, "zz-provider", fresh, other.Email, "other-person"); err != nil {
		t.Fatalf("LinkProvider: %v", err)
	}
	if err := st.TouchProvider(ctx, "zz-provider", fresh); err != nil {
		t.Fatalf("TouchProvider: %v", err)
	}
	linked, err := st.UserByProvider(ctx, "zz-provider", fresh)
	if err != nil {
		t.Fatalf("UserByProvider after linking: %v", err)
	}
	if linked.ID != other.ID {
		t.Errorf("id = %d, want %d", linked.ID, other.ID)
	}
}

func TestOneAccountHasOneIdentityPerProvider(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })

	user, err := st.CreateProviderUser(ctx, "zz-provider", uniqueSubject(), email,
		"Provider Person", email, "provider-person")
	if err != nil {
		t.Fatalf("CreateProviderUser: %v", err)
	}

	err = st.LinkProvider(ctx, user.ID, "zz-provider", uniqueSubject(), email, "another-login")
	if !errors.Is(err, ErrProviderTaken) {
		t.Errorf("a second identity for one provider = %v, want ErrProviderTaken", err)
	}
}

// testStoreUser makes an ordinary account for a test, the way the shell does.
func testStoreUser(t *testing.T, st *Store) *User {
	t.Helper()

	email := uniqueTestEmail()
	user, err := st.CreateUser(context.Background(), email, "Store Person", "subscriber", "stored-hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	return user
}
