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
	ID           int64      `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         string     `json:"role"`
	PasswordHash string     `json:"-"`
	DisabledAt   *time.Time `json:"disabledAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

func (u *User) Disabled() bool {
	return u.DisabledAt != nil
}

// UserView is what the admin screen shows: the account plus how much it owns,
// which is what the delete confirmation has to warn about.
type UserView struct {
	User
	Services int `json:"services"`
}

var ErrEmailTaken = errors.New("store: email already registered")

// Qualified, because the session lookup joins another table that also has a
// created_at. Insert statements alias the target table so the same list works.
const userColumns = `u.id, u.email, u.name, u.role, u.password_hash, u.disabled_at, u.created_at, u.updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash,
		&u.DisabledAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
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

func (s *Store) SetUserEmail(ctx context.Context, id int64, email string) error {
	tag, err := s.pool.Exec(ctx, `
		update users set email = $2, updated_at = now() where id = $1`, id, email)
	if isUniqueViolation(err) {
		return ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("users: set email %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetUserRole(ctx context.Context, id int64, role string) error {
	tag, err := s.pool.Exec(ctx, `
		update users set role = $2, updated_at = now() where id = $1`, id, role)
	if err != nil {
		return fmt.Errorf("users: set role %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetUserDisabled(ctx context.Context, id int64, disabled bool) error {
	tag, err := s.pool.Exec(ctx, `
		update users set disabled_at = case when $2 then now() else null end,
		                 updated_at = now()
		where id = $1`, id, disabled)
	if err != nil {
		return fmt.Errorf("users: set disabled %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `delete from users where id = $1`, id)
	if err != nil {
		return fmt.Errorf("users: delete %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		select `+userColumns+` from users u where u.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("users: by id %d: %w", id, err)
	}
	return u, nil
}

// ListUsers is the admin screen. role narrows it to one role and search matches
// the name or the email, which are the columns the list shows. It counts owned
// services, because deleting an account deletes them too.
func (s *Store) ListUsers(ctx context.Context, role, search string) ([]UserView, error) {
	rows, err := s.pool.Query(ctx, `
		select `+userColumns+`,
		       (select count(*) from services s where s.owner_id = u.id)
		from users u
		where ($1 = '' or u.role = $1)
		  and ($2 = ''
		       or position(lower($2) in lower(u.name)) > 0
		       or position(lower($2) in lower(u.email)) > 0)
		order by u.disabled_at is not null, u.name, u.email`, role, search)
	if err != nil {
		return nil, fmt.Errorf("users: list: %w", err)
	}
	defer rows.Close()

	var out []UserView
	for rows.Next() {
		var v UserView
		if err := rows.Scan(&v.ID, &v.Email, &v.Name, &v.Role, &v.PasswordHash,
			&v.DisabledAt, &v.CreatedAt, &v.UpdatedAt, &v.Services); err != nil {
			return nil, fmt.Errorf("users: scan: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountEnabledAdmins is what stops the last admin from being disabled, deleted
// or demoted, which would leave nobody able to manage anything.
func (s *Store) CountEnabledAdmins(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `
		select count(*) from users where role = 'admin' and disabled_at is null`).Scan(&n); err != nil {
		return 0, fmt.Errorf("users: count admins: %w", err)
	}
	return n, nil
}
