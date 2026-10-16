package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LimitAlert is how many vendors an account may watch at once. It is a key of
// the user_limits table, so a typo in it would be a row nothing reads.
const LimitAlert = "alert"

// UserLimit is the value one account is held to for one kind. The second result
// is false when there is no row, which lets the caller fall back to the default
// rather than read a missing row as no limit at all.
func (s *Store) UserLimit(ctx context.Context, userID int64, kind string) (int, bool, error) {
	var value int
	err := s.pool.QueryRow(ctx, `
		select max_value from user_limits where user_id = $1 and kind = $2`,
		userID, kind).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("limits: read %s for user %d: %w", kind, userID, err)
	}
	return value, true, nil
}

// SetUserLimits writes the values for one account, leaving the kinds it does not
// name alone. It is what the account screen will call; today only the defaults
// written when the account is made go through here.
func (s *Store) SetUserLimits(ctx context.Context, userID int64, limits map[string]int) error {
	if len(limits) == 0 {
		return nil
	}

	kinds := make([]string, 0, len(limits))
	values := make([]int, 0, len(limits))
	for kind, value := range limits {
		kinds = append(kinds, kind)
		values = append(values, value)
	}

	if _, err := s.pool.Exec(ctx, `
		insert into user_limits (user_id, kind, max_value)
		select $1, k.kind, k.max_value
		from unnest($2::text[], $3::int[]) as k (kind, max_value)
		on conflict (user_id, kind) do update set
			max_value  = excluded.max_value,
			updated_at = now()`,
		userID, kinds, values); err != nil {
		return fmt.Errorf("limits: write for user %d: %w", userID, err)
	}
	return nil
}

// insertDefaultLimits is the starting row for every kind, written in the same
// transaction that creates the account so an account never exists without the
// limits it is held to.
func (s *Store) insertDefaultLimits(ctx context.Context, tx pgx.Tx, userID int64) error {
	if len(s.defaultLimits) == 0 {
		return nil
	}

	kinds := make([]string, 0, len(s.defaultLimits))
	values := make([]int, 0, len(s.defaultLimits))
	for kind, value := range s.defaultLimits {
		kinds = append(kinds, kind)
		values = append(values, value)
	}

	if _, err := tx.Exec(ctx, `
		insert into user_limits (user_id, kind, max_value)
		select $1, k.kind, k.max_value
		from unnest($2::text[], $3::int[]) as k (kind, max_value)`,
		userID, kinds, values); err != nil {
		return fmt.Errorf("limits: defaults for user %d: %w", userID, err)
	}
	return nil
}
