package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned an empty hash")
	}
	if !CheckPassword(hash, "correct horse battery staple") {
		t.Error("the right password was rejected")
	}
	if CheckPassword(hash, "wrong password") {
		t.Error("a wrong password was accepted")
	}
	if CheckPassword(hash, "") {
		t.Error("an empty password was accepted")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	first, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Error("two hashes of the same password are identical, so they are not salted")
	}
}

func TestDummyHashMatchesNothing(t *testing.T) {
	hash := DummyHash()
	for _, guess := range []string{"", "downradar", "downradar-placeholder", "password"} {
		if CheckPassword(hash, guess) {
			t.Errorf("%q signed someone in", guess)
		}
	}
	if second := DummyHash(); second == hash {
		t.Error("two dummy hashes are identical, so the value they hide is fixed")
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Error("a short password was accepted")
	}
	if err := ValidatePassword(strings.Repeat("x", 73)); err == nil {
		t.Error("a password longer than bcrypt can hash was accepted")
	}
	if err := ValidatePassword(strings.Repeat("x", 72)); err != nil {
		t.Errorf("72 bytes should be accepted: %v", err)
	}
	if err := ValidatePassword("longenough"); err != nil {
		t.Errorf("8 characters should be accepted: %v", err)
	}
}

func TestNewTokenIsUniqueAndUrlSafe(t *testing.T) {
	seen := make(map[string]bool, 50)
	for i := 0; i < 50; i++ {
		token, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if len(token) < 40 {
			t.Errorf("token %q is shorter than expected", token)
		}
		if seen[token] {
			t.Fatalf("token %q was handed out twice", token)
		}
		seen[token] = true
		if strings.ContainsAny(token, "+/= ") {
			t.Errorf("token %q is not safe to put in a cookie", token)
		}
	}
}

func TestHashTokenIsStable(t *testing.T) {
	first := HashToken("a-token")
	if first != HashToken("a-token") {
		t.Error("hashing the same token twice gave different results")
	}
	if first == HashToken("another-token") {
		t.Error("two different tokens hashed to the same value")
	}
	if len(first) != 64 {
		t.Errorf("hash length = %d, want 64 hex characters", len(first))
	}
}

func TestRoles(t *testing.T) {
	for _, role := range Roles() {
		if !ValidRole(role) {
			t.Errorf("%q should be a valid role", role)
		}
	}
	for _, role := range []string{"", "owner", "Admin"} {
		if ValidRole(role) {
			t.Errorf("%q should not be a valid role", role)
		}
	}
}

func TestPermissions(t *testing.T) {
	if !CanManageServices(RoleAdmin) || !CanManageServices(RoleModerator) {
		t.Error("admin and moderator should manage services")
	}
	if CanManageServices(RoleSubscriber) {
		t.Error("a subscriber must not manage services")
	}
	if !CanRemovePublished(RoleAdmin) {
		t.Error("an admin should be able to remove a published service")
	}
	if CanRemovePublished(RoleModerator) {
		t.Error("a moderator must not be able to remove a published service")
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  Foo@Example.COM ": "foo@example.com",
		"bar@example.com":    "bar@example.com",
		"":                   "",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
