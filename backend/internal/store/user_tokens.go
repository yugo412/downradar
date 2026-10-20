package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The kinds of single use link an account needs. They are kept apart so a link
// that signs somebody in cannot be spent as a password reset, and a link that
// proves an address cannot be spent as either.
const (
	UserTokenVerify = "verify"
	UserTokenReset  = "reset"
	UserTokenLogin  = "login"
)

func UserTokenKindValid(kind string) bool {
	return kind == UserTokenVerify || kind == UserTokenReset || kind == UserTokenLogin
}

// CreateUserToken records one link. Any outstanding link of the same kind for
// that person is spent first: asking for another one is a reason for the old one
// to stop working, not for two of them to be live at once.
func (s *Store) CreateUserToken(ctx context.Context, userID int64, kind, tokenHash string, expiresAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("user tokens: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		update user_tokens set used_at = now()
		where user_id = $1 and kind = $2 and used_at is null`, userID, kind); err != nil {
		return fmt.Errorf("user tokens: retire older ones: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		insert into user_tokens (user_id, kind, token_hash, expires_at)
		values ($1, $2, $3, $4)`, userID, kind, tokenHash, expiresAt); err != nil {
		return fmt.Errorf("user tokens: create: %w", err)
	}

	return tx.Commit(ctx)
}

// UserByUserToken resolves a link to the person it belongs to. An expired or
// already spent token is treated as missing, so expiry needs no cleanup job to
// be correct. The token is spent by the caller, once it has done its work.
func (s *Store) UserByUserToken(ctx context.Context, kind, tokenHash string) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		select `+userColumns+`
		from user_tokens t
		join users u on u.id = t.user_id
		where t.token_hash = $1 and t.kind = $2
		  and t.used_at is null and t.expires_at > now()
		  and u.disabled_at is null`, tokenHash, kind))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("user tokens: resolve: %w", err)
	}
	return u, nil
}

// SpendUserToken marks a link as used. It reports whether it was still live, so
// a caller cannot act twice on the same one.
func (s *Store) SpendUserToken(ctx context.Context, kind, tokenHash string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		update user_tokens set used_at = now()
		where token_hash = $1 and kind = $2 and used_at is null and expires_at > now()`,
		tokenHash, kind)
	if err != nil {
		return false, fmt.Errorf("user tokens: spend: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) DeleteExpiredUserTokens(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `delete from user_tokens where expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("user tokens: delete expired: %w", err)
	}
	return tag.RowsAffected(), nil
}
