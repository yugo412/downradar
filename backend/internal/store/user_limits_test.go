package store

import (
	"context"
	"testing"
)

func TestCreateUserWritesTheStartingLimits(t *testing.T) {
	st := integrationStoreWith(t, Options{DefaultLimits: map[string]int{LimitAlert: 7}})
	ctx := context.Background()

	user, err := st.CreateUser(ctx, uniqueTestEmail(), "Limit Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteUser(ctx, user.ID) })

	got, ok, err := st.UserLimit(ctx, user.ID, LimitAlert)
	if err != nil {
		t.Fatalf("UserLimit: %v", err)
	}
	if !ok || got != 7 {
		t.Errorf("alert = %d (found %v), want the value it was made with", got, ok)
	}
}

func TestSetUserLimitsChangesOneAccountOnly(t *testing.T) {
	st := integrationStoreWith(t, Options{DefaultLimits: map[string]int{LimitAlert: 10}})
	ctx := context.Background()

	one, err := st.CreateUser(ctx, uniqueTestEmail(), "One", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteUser(ctx, one.ID) })

	two, err := st.CreateUser(ctx, uniqueTestEmail(), "Two", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteUser(ctx, two.ID) })

	if err := st.SetUserLimits(ctx, one.ID, map[string]int{LimitAlert: 25}); err != nil {
		t.Fatalf("SetUserLimits: %v", err)
	}

	if got, _, _ := st.UserLimit(ctx, one.ID, LimitAlert); got != 25 {
		t.Errorf("one = %d, want 25", got)
	}
	if got, _, _ := st.UserLimit(ctx, two.ID, LimitAlert); got != 10 {
		t.Errorf("two = %d, want the value it was made with", got)
	}
}

func TestDeletingAnAccountTakesItsLimits(t *testing.T) {
	st := integrationStoreWith(t, Options{DefaultLimits: map[string]int{LimitAlert: 10}})
	ctx := context.Background()

	user, err := st.CreateUser(ctx, uniqueTestEmail(), "Gone", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if _, ok, err := st.UserLimit(ctx, user.ID, LimitAlert); err != nil || ok {
		t.Errorf("a deleted account still has a limit (found %v, err %v)", ok, err)
	}
}

// An account with no row still has a limit: the caller falls back to the code
// default rather than reading a missing row as no limit at all.
func TestUserLimitReportsAMissingRow(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user, err := st.CreateUser(ctx, uniqueTestEmail(), "No Row", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteUser(ctx, user.ID) })

	if _, ok, err := st.UserLimit(ctx, user.ID, LimitAlert); err != nil || ok {
		t.Errorf("found = %v, err = %v, want a missing row", ok, err)
	}
}
