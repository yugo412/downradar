package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The three things a subscription can be doing. They are stored as two
// timestamps, and this is what the two of them say.
const (
	AlertActive       = "active"
	AlertPaused       = "paused"
	AlertUnsubscribed = "unsubscribed"
)

// AlertStateValid reports whether a state is one a caller may ask for.
func AlertStateValid(state string) bool {
	switch state {
	case AlertActive, AlertPaused, AlertUnsubscribed:
		return true
	default:
		return false
	}
}

// AlertView is one subscription: what it is for, where it goes, and what it is
// doing. The address is carried so a handler can mask it or show it, and the id
// so the account screen can act on the row. VendorStatus is the vendor's own
// health, which the account screen shows so a row says more than its name.
type AlertView struct {
	ID             int64      `json:"id"`
	VendorSlug     string     `json:"vendorSlug"`
	VendorName     string     `json:"vendorName"`
	VendorStatus   string     `json:"vendorStatus,omitempty"`
	Email          string     `json:"-"`
	CreatedAt      time.Time  `json:"createdAt"`
	PausedAt       *time.Time `json:"pausedAt,omitempty"`
	UnsubscribedAt *time.Time `json:"unsubscribedAt,omitempty"`
}

// State is what the row is doing now.
func (v AlertView) State() string {
	switch {
	case v.UnsubscribedAt != nil:
		return AlertUnsubscribed
	case v.PausedAt != nil:
		return AlertPaused
	default:
		return AlertActive
	}
}

// UserAlerts is everything one account has asked to be told about, newest first.
// Rows that are paused or were unsubscribed are part of the list: the point of
// the screen is to show them and offer to start them again. Each one carries the
// vendor's current health, so a row can say whether the thing it watches is up.
func (s *Store) UserAlerts(ctx context.Context, userID int64) ([]AlertView, error) {
	rows, err := s.pool.Query(ctx, `
		select a.id, v.slug, v.name, coalesce(ss.status, 'unknown'),
		       a.email, a.created_at, a.paused_at, a.unsubscribed_at
		from alert_subscriptions a
		join services v on v.id = a.service_id
		left join service_state ss on ss.service_id = v.id
		where a.user_id = $1
		order by a.id desc`, userID)
	if err != nil {
		return nil, fmt.Errorf("alerts: list for user %d: %w", userID, err)
	}
	defer rows.Close()

	var out []AlertView
	for rows.Next() {
		var v AlertView
		if err := rows.Scan(&v.ID, &v.VendorSlug, &v.VendorName, &v.VendorStatus,
			&v.Email, &v.CreatedAt, &v.PausedAt, &v.UnsubscribedAt); err != nil {
			return nil, fmt.Errorf("alerts: scan list: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetUserAlertState moves one of an account's own subscriptions between active,
// paused and unsubscribed. Anything waiting for an address that has just been
// paused or ended is dropped: the point of both is that no further email
// arrives, and resuming should not deliver last week's news.
func (s *Store) SetUserAlertState(ctx context.Context, userID, id int64, state string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("alerts: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var updated int64
	err = tx.QueryRow(ctx, `
		update alert_subscriptions set
			unsubscribed_at = case when $3 = 'unsubscribed' then now() else null end,
			paused_at       = case when $3 = 'paused' then now() else null end,
			updated_at      = now()
		where id = $1 and user_id = $2
		returning id`, id, userID, state).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("alerts: set state for %d: %w", id, err)
	}

	if state != AlertActive {
		if _, err := tx.Exec(ctx, `
			delete from alert_deliveries
			where sent_at is null and subscription_id = $1`, updated); err != nil {
			return fmt.Errorf("alerts: drop pending deliveries: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("alerts: set state commit: %w", err)
	}
	return nil
}

// DeleteUserAlert removes one of an account's own subscriptions for good. The
// unsubscribe link keeps a row so the same address can be subscribed again; this
// is for somebody who wants it gone from the screen.
func (s *Store) DeleteUserAlert(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx,
		`delete from alert_subscriptions where id = $1 and user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("alerts: delete %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingAlert is one email waiting to go out. It carries the change it is
// about, so an alert describes the moment it happened even if the status has
// moved on by the time the mail server takes it.
type PendingAlert struct {
	ID         int64
	Email      string
	VendorName string
	VendorSlug string
	Token      string
	FromStatus string
	ToStatus   string
	ChangedAt  time.Time
	Attempts   int
}

// PublicService returns the id and name of a vendor the open API can serve,
// which is the only kind an alert can be subscribed to: the email links to the
// vendor page, and a private service has none.
func (s *Store) PublicService(ctx context.Context, slug string) (int64, string, error) {
	var id int64
	var name string
	err := s.pool.QueryRow(ctx, `
		select id, name from services
		where slug = $1 and active and status = 'published' and owner_id is null`,
		slug,
	).Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	if err != nil {
		return 0, "", fmt.Errorf("alerts: public service %s: %w", slug, err)
	}
	return id, name, nil
}

// SubscribeAlert records one address against one service. An address that
// unsubscribed before is subscribed again rather than refused, and keeps its
// token, so an old email's unsubscribe link still reaches this row. A user id is
// only kept if one is given, so subscribing while signed out never clears a link
// that was made while signed in.
func (s *Store) SubscribeAlert(ctx context.Context, serviceID int64, userID *int64, email, token string) error {
	_, err := s.pool.Exec(ctx, `
		insert into alert_subscriptions (service_id, user_id, email, token)
		values ($1, $2, $3, $4)
		on conflict (service_id, email) do update set
			user_id         = coalesce(excluded.user_id, alert_subscriptions.user_id),
			unsubscribed_at = null,
			updated_at      = now()`,
		serviceID, userID, email, token)
	if err != nil {
		return fmt.Errorf("alerts: subscribe %s: %w", email, err)
	}
	return nil
}

// VendorRef names a vendor, which is all a caller needs to point somebody at it.
type VendorRef struct {
	Name string
	Slug string
}

// AlertElsewhere returns the vendor an address is already subscribed to, other
// than the one being asked about, or ErrNotFound when it has none. It is what a
// visitor who is not signed in is held to: with no account behind them, the
// address they typed is their whole identity, so it is the only thing there is
// to count. The vendor is named so the answer can say which one, rather than
// leaving somebody to guess.
//
// Only alerts that can still arrive count. A vendor that has been removed from
// the catalogue is not polled any more, so the alert on it is not holding the
// place that a second one would take.
func (s *Store) AlertElsewhere(ctx context.Context, email string, serviceID int64) (*VendorRef, error) {
	var ref VendorRef
	err := s.pool.QueryRow(ctx, `
		select v.name, v.slug
		from alert_subscriptions a
		join services v on v.id = a.service_id
		where a.email = $1 and a.unsubscribed_at is null and a.service_id <> $2
		  and v.active and v.status = 'published' and v.owner_id is null
		order by a.id
		limit 1`, email, serviceID).Scan(&ref.Name, &ref.Slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: elsewhere for %s: %w", email, err)
	}
	return &ref, nil
}

// ActiveAlertCount is how many alerts this account still has that can arrive,
// leaving out one vendor so re-subscribing to it is not counted twice. A vendor
// removed from the catalogue is not polled any more, so its alert is not holding
// a place. It is what caps a subscriber; guests are held by AlertElsewhere and
// staff are not held at all.
func (s *Store) ActiveAlertCount(ctx context.Context, userID, exceptServiceID int64) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		select count(*)
		from alert_subscriptions a
		join services v on v.id = a.service_id
		where a.user_id = $1 and a.unsubscribed_at is null and a.service_id <> $2
		  and v.active and v.status = 'published' and v.owner_id is null`,
		userID, exceptServiceID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("alerts: active count for user %d: %w", userID, err)
	}
	return count, nil
}

// AlertByToken resolves the token in an unsubscribe link.
func (s *Store) AlertByToken(ctx context.Context, token string) (*AlertView, error) {
	var view AlertView
	err := s.pool.QueryRow(ctx, `
		select s.slug, s.name, a.email, a.created_at, a.unsubscribed_at
		from alert_subscriptions a
		join services s on s.id = a.service_id
		where a.token = $1`, token,
	).Scan(&view.VendorSlug, &view.VendorName, &view.Email, &view.CreatedAt, &view.UnsubscribedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: by token: %w", err)
	}
	return &view, nil
}

// UnsubscribeAlert stops the alerts for one token. It is written to be safe to
// repeat: the second visit changes nothing and is not an error.
func (s *Store) UnsubscribeAlert(ctx context.Context, token string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("alerts: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id int64
	err = tx.QueryRow(ctx, `
		update alert_subscriptions
		set unsubscribed_at = now(), updated_at = now()
		where token = $1
		returning id`, token).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("alerts: unsubscribe: %w", err)
	}

	// Anything still waiting for this address is dropped: the point of
	// unsubscribing is that no further email arrives.
	if _, err := tx.Exec(ctx, `
		delete from alert_deliveries
		where subscription_id = $1 and sent_at is null`, id); err != nil {
		return fmt.Errorf("alerts: drop pending deliveries: %w", err)
	}

	return tx.Commit(ctx)
}

// UserAlert returns the alert a signed in person holds on one vendor, or
// ErrNotFound when they hold none. It is what the bell reads to decide whether
// to offer an alert or to say it already has one, so it looks at the link to the
// account rather than at the address: the address they gave does not have to be
// the one on their account. A paused one still counts as held.
func (s *Store) UserAlert(ctx context.Context, userID, serviceID int64) (*AlertView, error) {
	var view AlertView
	err := s.pool.QueryRow(ctx, `
		select a.id, v.slug, v.name, a.email, a.created_at, a.paused_at, a.unsubscribed_at
		from alert_subscriptions a
		join services v on v.id = a.service_id
		where a.user_id = $1 and a.service_id = $2 and a.unsubscribed_at is null
		order by a.id
		limit 1`, userID, serviceID,
	).Scan(&view.ID, &view.VendorSlug, &view.VendorName, &view.Email,
		&view.CreatedAt, &view.PausedAt, &view.UnsubscribedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: for user %d on service %d: %w", userID, serviceID, err)
	}
	return &view, nil
}

// UnsubscribeUserAlert stops every alert a signed in person holds on one vendor,
// and reports how many that was. Alerts they made while signed out are left
// alone: those belong to the address, not to the account.
func (s *Store) UnsubscribeUserAlert(ctx context.Context, userID, serviceID int64) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("alerts: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		update alert_subscriptions
		set unsubscribed_at = now(), paused_at = null, updated_at = now()
		where user_id = $1 and service_id = $2 and unsubscribed_at is null
		returning id`, userID, serviceID)
	if err != nil {
		return 0, fmt.Errorf("alerts: unsubscribe user %d: %w", userID, err)
	}

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("alerts: scan unsubscribed: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("alerts: unsubscribed rows: %w", err)
	}

	// Nothing waiting for those addresses should still go out: the point of
	// unsubscribing is that no further email arrives.
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `
			delete from alert_deliveries
			where sent_at is null and subscription_id = any($1)`, ids); err != nil {
			return 0, fmt.Errorf("alerts: drop pending deliveries: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("alerts: unsubscribe commit: %w", err)
	}
	return len(ids), nil
}

// PendingAlerts is the next batch for the sender, oldest first.
func (s *Store) PendingAlerts(ctx context.Context, maxAttempts, limit int) ([]PendingAlert, error) {
	rows, err := s.pool.Query(ctx, `
		select d.id, a.email, s.name, s.slug, a.token,
		       d.from_status, d.to_status, d.changed_at, d.attempts
		from alert_deliveries d
		join alert_subscriptions a on a.id = d.subscription_id
		join services s on s.id = a.service_id
		where d.sent_at is null and d.attempts < $1 and a.unsubscribed_at is null
		  and a.paused_at is null
		order by d.id
		limit $2`, maxAttempts, limit)
	if err != nil {
		return nil, fmt.Errorf("alerts: pending: %w", err)
	}
	defer rows.Close()

	var out []PendingAlert
	for rows.Next() {
		var p PendingAlert
		if err := rows.Scan(&p.ID, &p.Email, &p.VendorName, &p.VendorSlug, &p.Token,
			&p.FromStatus, &p.ToStatus, &p.ChangedAt, &p.Attempts); err != nil {
			return nil, fmt.Errorf("alerts: scan pending: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SentAlert marks one email as delivered.
func (s *Store) SentAlert(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx, `
		update alert_deliveries set sent_at = now(), error = null
		where id = $1`, id); err != nil {
		return fmt.Errorf("alerts: mark sent: %w", err)
	}
	return nil
}

// FailedAlert records one failed attempt. The row keeps its error and its count,
// which is what stops a dead address from being retried for ever.
func (s *Store) FailedAlert(ctx context.Context, id int64, message string) error {
	if _, err := s.pool.Exec(ctx, `
		update alert_deliveries set attempts = attempts + 1, error = $2
		where id = $1`, id, message); err != nil {
		return fmt.Errorf("alerts: mark failed: %w", err)
	}
	return nil
}
