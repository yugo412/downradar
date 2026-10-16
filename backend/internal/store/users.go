package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

var ErrEmailTaken = errors.New("store: email already registered")

// Qualified, because the session lookup joins another table that also has a
// created_at. Insert statements alias the target table so the same list works.
const userColumns = `u.id, u.email, u.name, u.role, u.password_hash, u.created_at, u.updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *Store) CreateUser(ctx context.Context, email, name, role, passwordHash string) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		insert into users as u (email, name, role, password_hash)
		values ($1, $2, $3, $4)
		returning `+userColumns, email, name, role, passwordHash))
	if isUniqueViolation(err) {
		return nil, ErrEmailTaken
	}
	if err != nil {
		return nil, fmt.Errorf("users: create %s: %w", email, err)
	}
	return u, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		select `+userColumns+` from users u where lower(u.email) = lower($1)`, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("users: by email %s: %w", email, err)
	}
	return u, nil
}

func (s *Store) SetUserPassword(ctx context.Context, id int64, passwordHash string) error {
	tag, err := s.pool.Exec(ctx, `
		update users set password_hash = $2, updated_at = now() where id = $1`, id, passwordHash)
	if err != nil {
		return fmt.Errorf("users: set password %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetUserName(ctx context.Context, id int64, name string) error {
	tag, err := s.pool.Exec(ctx, `
		update users set name = $2, updated_at = now() where id = $1`, id, name)
	if err != nil {
		return fmt.Errorf("users: set name %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
