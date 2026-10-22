package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrProviderTaken is a provider identity that is already linked, either to this
// account with another identity of the same provider or to somebody else.
var ErrProviderTaken = errors.New("store: that provider account is already linked")

const (
	providerIdentityKey = "user_providers_provider_key"
	providerUserKey     = "user_providers_user_key"
)

// isConstraintViolation says whether err came from one named unique constraint,
// which is how a duplicate is told apart from a failure.
func isConstraintViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// UserByProvider finds the account a provider identity was linked to.
func (s *Store) UserByProvider(ctx context.Context, provider, subject string) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		select `+userColumns+` from users u
		join user_providers p on p.user_id = u.id
		where p.provider = $1 and p.subject = $2`, provider, subject))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: user by %s identity: %w", provider, err)
	}
	return u, nil
}

// LinkProvider records a provider identity against an account that already
// existed, which is what happens when somebody signs in using an address the site
// knows. An identity linked elsewhere is refused rather than moved.
func (s *Store) LinkProvider(ctx context.Context, userID int64, provider, subject, email, username string) error {
	tag, err := s.pool.Exec(ctx, `
		insert into user_providers (user_id, provider, subject, email, username, last_login_at)
		values ($1, $2, $3, $4, $5, now())
		on conflict on constraint `+providerIdentityKey+` do update
			set email = excluded.email,
			    username = excluded.username,
			    last_login_at = now()
			where user_providers.user_id = excluded.user_id`,
		userID, provider, subject, email, username)
	if isConstraintViolation(err, providerUserKey) {
		return ErrProviderTaken
	}
	if err != nil {
		return fmt.Errorf("store: link a %s identity to user %d: %w", provider, userID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrProviderTaken
	}
	return nil
}

// TouchProvider records that the identity signed in again.
func (s *Store) TouchProvider(ctx context.Context, provider, subject string) error {
	tag, err := s.pool.Exec(ctx, `
		update user_providers set last_login_at = now()
		where provider = $1 and subject = $2`, provider, subject)
	if err != nil {
		return fmt.Errorf("store: touch a %s identity: %w", provider, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateProviderUser makes an account from what a provider vouches for: the
// account, its starting limits and the identity link in one transaction. It is
// always a subscriber, and its address counts as confirmed by the provider.
func (s *Store) CreateProviderUser(ctx context.Context, provider, subject, email, name, providerEmail, username string) (*User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: create provider user begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	u, err := scanUser(tx.QueryRow(ctx, `
		insert into users as u (email, name, role, password_hash, verified_at)
		values ($1, $2, 'subscriber', '', now())
		returning `+userColumns, email, name))
	if isUniqueViolation(err) {
		return nil, ErrEmailTaken
	}
	if err != nil {
		return nil, fmt.Errorf("store: create user %s from %s: %w", email, provider, err)
	}

	if err := s.insertDefaultLimits(ctx, tx, u.ID); err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `
		insert into user_providers (user_id, provider, subject, email, username, last_login_at)
		values ($1, $2, $3, $4, $5, now())`,
		u.ID, provider, subject, providerEmail, username)
	if isUniqueViolation(err) {
		return nil, ErrProviderTaken
	}
	if err != nil {
		return nil, fmt.Errorf("store: link a %s identity to a new user: %w", provider, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: create provider user commit: %w", err)
	}
	return u, nil
}
