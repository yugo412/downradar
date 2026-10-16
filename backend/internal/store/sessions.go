package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error {
	if _, err := s.pool.Exec(ctx, `
		insert into sessions (token_hash, user_id, expires_at)
		values ($1, $2, $3)`, tokenHash, userID, expiresAt); err != nil {
		return fmt.Errorf("sessions: create: %w", err)
	}
	return nil
}

// SessionUser resolves a session token to the person behind it. An expired row
// is treated as missing, so expiry needs no cleanup job to be correct.
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		select `+userColumns+`
		from sessions s
		join users u on u.id = s.user_id
		where s.token_hash = $1 and s.expires_at > now() and u.disabled_at is null`, tokenHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sessions: resolve: %w", err)
	}
	return u, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.pool.Exec(ctx, `
		delete from sessions where token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("sessions: delete: %w", err)
	}
	return nil
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		delete from sessions where user_id = $1`, userID)
	if err != nil {
		return 0, fmt.Errorf("sessions: delete for user %d: %w", userID, err)
	}
	return tag.RowsAffected(), nil
}

// DeleteOtherSessions ends every session of a user except the one given. Used
// after a password change, so the person stays signed in where they are and
// everywhere else is signed out.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID int64, keepTokenHash string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		delete from sessions where user_id = $1 and token_hash <> $2`, userID, keepTokenHash)
	if err != nil {
		return 0, fmt.Errorf("sessions: delete others for user %d: %w", userID, err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		delete from sessions where expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("sessions: delete expired: %w", err)
	}
	return tag.RowsAffected(), nil
}
