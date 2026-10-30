package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"downradar.info/backend/internal/auth"
)

// The two audiences a notice can be written for. A caller that needs a specific
// set of people can name them instead, which is what the recipient list is for.
const (
	AudienceStaff       = "staff"
	AudienceSubscribers = "subscribers"
)

// The notice is a short thing to read, not a log page. A caller that hands over
// a long error is clipped here rather than trusted to keep it short.
const (
	maxNotificationTitle = 160
	maxNotificationBody  = 4000
)

// NotificationInput is one thing to put into the feed. Audience is used unless
// Recipients is given, so an audience-wide notice and a notice to a particular
// account take the same path. The links are optional; the notice stays if the
// service or alert is later removed, though the link is cleared with it.
type NotificationInput struct {
	Audience   string
	Recipients []int64
	Title      string
	Body       string
	ServiceID  *int64
	AlertID    *int64
	CreatedBy  *int64
	// DedupeKey makes one event that arrives through several alert channels
	// write a single notice. An empty key is not deduplicated.
	DedupeKey string
}

// NotificationView is one notice as its recipient sees it. ServiceSlug and
// ServiceName are for a link to the vendor; AlertID and AlertVendorSlug are for
// a link to the alert that earned it. ReadAt is nil until the account opens it.
type NotificationView struct {
	ID              int64      `json:"id"`
	Audience        string     `json:"audience"`
	Title           string     `json:"title"`
	Body            string     `json:"body"`
	ServiceSlug     string     `json:"serviceSlug,omitempty"`
	ServiceName     string     `json:"serviceName,omitempty"`
	AlertID         *int64     `json:"alertId,omitempty"`
	AlertVendorSlug string     `json:"alertVendorSlug,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	ReadAt          *time.Time `json:"readAt,omitempty"`
}

// clipText keeps one long error from turning a notice into a wall. It counts
// runes so a multibyte character is never cut in half.
func clipText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

// CreateNotification writes one notice and gives it to its recipients. A notice
// with nobody to read it is not written at all, so the feed does not grow rows
// that no page can reach. A DedupeKey that was already written is a no-op.
func (s *Store) CreateNotification(ctx context.Context, in NotificationInput) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("notifications: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	written, err := s.createNotificationTx(ctx, tx, in)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("notifications: commit: %w", err)
	}
	return written, nil
}

// createNotificationTx is the body of CreateNotification, kept apart so the
// caller owns the commit.
func (s *Store) createNotificationTx(ctx context.Context, tx pgx.Tx, in NotificationInput) (int, error) {
	in.Title = clipText(in.Title, maxNotificationTitle)
	in.Body = clipText(in.Body, maxNotificationBody)
	if in.Title == "" {
		return 0, errors.New("notifications: a title is required")
	}
	if in.Body == "" {
		return 0, errors.New("notifications: a body is required")
	}
	if in.Audience != AudienceStaff && in.Audience != AudienceSubscribers {
		return 0, fmt.Errorf("notifications: unknown audience %q", in.Audience)
	}

	recipients, err := notificationRecipients(ctx, tx, in)
	if err != nil {
		return 0, err
	}
	if len(recipients) == 0 {
		return 0, nil
	}

	var dedupe any
	if in.DedupeKey != "" {
		dedupe = in.DedupeKey
	}

	var id int64
	err = tx.QueryRow(ctx, `
		insert into notifications (audience, title, body, service_id, alert_id, created_by, dedupe_key)
		values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (dedupe_key) do nothing
		returning id`,
		in.Audience, in.Title, in.Body, in.ServiceID, in.AlertID, in.CreatedBy, dedupe,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("notifications: insert: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		insert into notification_recipients (notification_id, user_id)
		select $1, unnest($2::bigint[])`, id, recipients); err != nil {
		return 0, fmt.Errorf("notifications: recipients: %w", err)
	}
	return len(recipients), nil
}

// notificationRecipients resolves who a notice is for. Disabled accounts are
// left out: they cannot sign in to read it. An explicit list wins over the
// audience, which is what lets another part of the app address one account.
func notificationRecipients(ctx context.Context, tx pgx.Tx, in NotificationInput) ([]int64, error) {
	rows, err := func() (pgx.Rows, error) {
		if len(in.Recipients) > 0 {
			return tx.Query(ctx, `
				select id from users
				where id = any($1) and disabled_at is null
				order by id`, in.Recipients)
		}

		var roles []string
		switch in.Audience {
		case AudienceStaff:
			roles = []string{auth.RoleAdmin, auth.RoleModerator}
		case AudienceSubscribers:
			roles = []string{auth.RoleSubscriber}
		default:
			return nil, fmt.Errorf("notifications: unknown audience %q", in.Audience)
		}
		return tx.Query(ctx, `
			select id from users
			where role = any($1) and disabled_at is null
			order by id`, roles)
	}()
	if err != nil {
		return nil, fmt.Errorf("notifications: recipients: %w", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("notifications: scan recipient: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Notifications is one account's feed, newest first. The limit is the caller's,
// so the page fetches only what it shows.
func (s *Store) Notifications(ctx context.Context, userID int64, limit int) ([]NotificationView, error) {
	rows, err := s.pool.Query(ctx, `
		select n.id, n.audience, n.title, n.body,
		       coalesce(s.slug, ''), coalesce(s.name, ''),
		       n.alert_id, coalesce(av.slug, ''),
		       n.created_at, r.read_at
		from notification_recipients r
		join notifications n on n.id = r.notification_id
		left join services s on s.id = n.service_id
		left join alert_subscriptions a on a.id = n.alert_id
		left join services av on av.id = a.service_id
		where r.user_id = $1
		order by n.created_at desc, n.id desc
		limit $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("notifications: feed for user %d: %w", userID, err)
	}
	defer rows.Close()

	out := []NotificationView{}
	for rows.Next() {
		var n NotificationView
		if err := rows.Scan(&n.ID, &n.Audience, &n.Title, &n.Body, &n.ServiceSlug,
			&n.ServiceName, &n.AlertID, &n.AlertVendorSlug, &n.CreatedAt,
			&n.ReadAt); err != nil {
			return nil, fmt.Errorf("notifications: scan feed: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnreadNotifications is what the bell beside the menu counts.
func (s *Store) UnreadNotifications(ctx context.Context, userID int64) (int, error) {
	var unread int
	if err := s.pool.QueryRow(ctx, `
		select count(*) from notification_recipients
		where user_id = $1 and read_at is null`, userID).Scan(&unread); err != nil {
		return 0, fmt.Errorf("notifications: unread for user %d: %w", userID, err)
	}
	return unread, nil
}

// SetNotificationRead marks one notice read, or unread again, for one account.
// It changes nothing when the notice is not on that account's feed.
func (s *Store) SetNotificationRead(ctx context.Context, userID, id int64, read bool) error {
	tag, err := s.pool.Exec(ctx, `
		update notification_recipients
		set read_at = case when $3 then coalesce(read_at, now()) else null end
		where notification_id = $1 and user_id = $2`, id, userID, read)
	if err != nil {
		return fmt.Errorf("notifications: set read %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteNotification removes one notice from one account's feed. A notice is
// shared with its other recipients, so only this account's row goes away.
func (s *Store) DeleteNotification(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `
		delete from notification_recipients
		where notification_id = $1 and user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("notifications: delete %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAllNotificationsRead marks every unread notice in one feed read. It returns
// how many rows moved.
func (s *Store) SetAllNotificationsRead(ctx context.Context, userID int64) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		update notification_recipients
		set read_at = now()
		where user_id = $1 and read_at is null`, userID)
	if err != nil {
		return 0, fmt.Errorf("notifications: mark all read: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
