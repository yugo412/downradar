package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	RoleAdmin      = "admin"
	RoleModerator  = "moderator"
	RoleSubscriber = "subscriber"
)

func Roles() []string {
	return []string{RoleAdmin, RoleModerator, RoleSubscriber}
}

func ValidRole(role string) bool {
	switch role {
	case RoleAdmin, RoleModerator, RoleSubscriber:
		return true
	default:
		return false
	}
}

// CanManageServices is the gate for the whole service menu: adding, editing,
// approving, and rejecting. Deleting a published vendor is narrower, see
// CanRemovePublished.
func CanManageServices(role string) bool {
	return role == RoleAdmin || role == RoleModerator
}

func CanRemovePublished(role string) bool {
	return role == RoleAdmin
}

const bcryptCost = 12

func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(hash), nil
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// DummyHash returns a hash of a value nobody knows, used to spend the same time
// on a login attempt for an address that does not exist. Without it the
// response time alone tells an attacker which addresses are registered.
func DummyHash() string {
	const broken = "$2a$12$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidin"

	secret, err := NewToken()
	if err != nil {
		return broken
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcryptCost)
	if err != nil {
		return broken
	}
	return string(hash)
}

const MinPasswordLength = 8

func ValidatePassword(plain string) error {
	if len(plain) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(plain) > 72 {
		return fmt.Errorf("password must be at most 72 bytes")
	}
	return nil
}

const tokenBytes = 32

func NewToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
