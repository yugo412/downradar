package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/auth"
)

// isolatedNotificationStore gives these tests a schema of their own. A notice
// can reach a whole audience and read state is per account, so sharing a
// database with another test package would make the feed depend on timing.
func isolatedNotificationStore(t *testing.T) *Store {
	t.Helper()

	admin := integrationStore(t)
	ctx := context.Background()
	schema := fmt.Sprintf("zz_notice_store_%d", time.Now().UnixNano())
	if _, err := admin.pool.Exec(ctx, `create schema `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.pool.Exec(context.Background(), `drop schema `+schema+` cascade`)
	})

	dsn := os.Getenv("DATABASE_URL")
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	st, err := New(ctx, dsn+separator+"search_path="+schema, Options{AppKey: testAppKey})
	if err != nil {
		t.Fatalf("connect to isolated schema: %v", err)
	}
	t.Cleanup(st.Close)

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate isolated schema: %v", err)
	}
	return st
}

func noticeUser(t *testing.T, st *Store, role string) *User {
	t.Helper()

	user, err := st.CreateUser(context.Background(), uniqueTestEmail(), "Notice Person", role, "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteUser(context.Background(), user.ID) })
	return user
}

func findNotification(t *testing.T, list []NotificationView, title string) *NotificationView {
	t.Helper()

	for i := range list {
		if list[i].Title == title {
			return &list[i]
		}
	}
	return nil
}

func TestNotificationsFollowTheAudienceAndReadState(t *testing.T) {
	st := isolatedNotificationStore(t)
	ctx := context.Background()

	admin := noticeUser(t, st, auth.RoleAdmin)
	moderator := noticeUser(t, st, auth.RoleModerator)
	subscriber := noticeUser(t, st, auth.RoleSubscriber)

	title := uniqueName("zz-notice-staff")
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `delete from notifications where title = $1`, title)
	})

	written, err := st.CreateNotification(ctx, NotificationInput{
		Audience: AudienceStaff,
		Title:    title,
		Body:     "The poller could not read a status page.",
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if written < 2 {
		t.Fatalf("wrote %d staff rows, want at least 2", written)
	}

	adminFeed, err := st.Notifications(ctx, admin.ID, 100)
	if err != nil {
		t.Fatalf("Notifications admin: %v", err)
	}
	item := findNotification(t, adminFeed, title)
	if item == nil {
		t.Fatal("the admin did not receive the staff notice")
	}
	if item.ReadAt != nil {
		t.Error("a new notice is already read")
	}

	moderatorFeed, err := st.Notifications(ctx, moderator.ID, 100)
	if err != nil {
		t.Fatalf("Notifications moderator: %v", err)
	}
	if findNotification(t, moderatorFeed, title) == nil {
		t.Error("the moderator did not receive the staff notice")
	}

	subscriberFeed, err := st.Notifications(ctx, subscriber.ID, 100)
	if err != nil {
		t.Fatalf("Notifications subscriber: %v", err)
	}
	if findNotification(t, subscriberFeed, title) != nil {
		t.Error("a subscriber received a staff notice")
	}

	updated, err := st.SetAllNotificationsRead(ctx, admin.ID)
	if err != nil {
		t.Fatalf("SetAllNotificationsRead: %v", err)
	}
	if updated < 1 {
		t.Errorf("marked %d rows read, want at least 1", updated)
	}
	unread, err := st.UnreadNotifications(ctx, admin.ID)
	if err != nil {
		t.Fatalf("UnreadNotifications: %v", err)
	}
	if unread != 0 {
		t.Errorf("unread = %d after marking all read, want 0", unread)
	}

	adminFeed, err = st.Notifications(ctx, admin.ID, 100)
	if err != nil {
		t.Fatalf("Notifications after read: %v", err)
	}
	item = findNotification(t, adminFeed, title)
	if item == nil || item.ReadAt == nil {
		t.Fatal("the notice is not marked read")
	}

	if err := st.SetNotificationRead(ctx, admin.ID, item.ID, false); err != nil {
		t.Fatalf("SetNotificationRead(false): %v", err)
	}
	unread, err = st.UnreadNotifications(ctx, admin.ID)
	if err != nil {
		t.Fatalf("UnreadNotifications after unread: %v", err)
	}
	if unread != 1 {
		t.Errorf("unread = %d after marking one unread, want 1", unread)
	}

	if err := st.SetNotificationRead(ctx, subscriber.ID, item.ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("a stranger reading the notice = %v, want ErrNotFound", err)
	}
}

func TestNotificationsCanBeAddressedToOneAccount(t *testing.T) {
	st := isolatedNotificationStore(t)
	ctx := context.Background()

	admin := noticeUser(t, st, auth.RoleAdmin)
	subscriber := noticeUser(t, st, auth.RoleSubscriber)

	title := uniqueName("zz-notice-direct")
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `delete from notifications where title = $1`, title)
	})

	written, err := st.CreateNotification(ctx, NotificationInput{
		Audience:   AudienceSubscribers,
		Recipients: []int64{subscriber.ID},
		Title:      title,
		Body:       "This one is only for you.",
	})
	if err != nil {
		t.Fatalf("direct notice: %v", err)
	}
	if written != 1 {
		t.Errorf("wrote %d direct rows, want 1", written)
	}

	subscriberFeed, err := st.Notifications(ctx, subscriber.ID, 100)
	if err != nil {
		t.Fatalf("Notifications subscriber: %v", err)
	}
	if findNotification(t, subscriberFeed, title) == nil {
		t.Error("the named recipient did not receive the notice")
	}

	adminFeed, err := st.Notifications(ctx, admin.ID, 100)
	if err != nil {
		t.Fatalf("Notifications admin: %v", err)
	}
	if findNotification(t, adminFeed, title) != nil {
		t.Error("an unnamed account received the direct notice")
	}
}

func TestNotificationDedupeKeyWritesOneNotice(t *testing.T) {
	st := isolatedNotificationStore(t)
	ctx := context.Background()

	subscriber := noticeUser(t, st, auth.RoleSubscriber)
	title := uniqueName("zz-notice-dedupe")
	key := uniqueName("zz-event")
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `delete from notifications where title = $1`, title)
	})

	in := NotificationInput{
		Audience:   AudienceSubscribers,
		Recipients: []int64{subscriber.ID},
		Title:      title,
		Body:       "The same event reached two channels.",
		DedupeKey:  key,
	}
	written, err := st.CreateNotification(ctx, in)
	if err != nil {
		t.Fatalf("first CreateNotification: %v", err)
	}
	if written != 1 {
		t.Fatalf("first write = %d rows, want 1", written)
	}

	written, err = st.CreateNotification(ctx, in)
	if err != nil {
		t.Fatalf("second CreateNotification: %v", err)
	}
	if written != 0 {
		t.Errorf("second write = %d rows, want 0", written)
	}

	feed, err := st.Notifications(ctx, subscriber.ID, 100)
	if err != nil {
		t.Fatalf("Notifications: %v", err)
	}
	if got := countNotifications(feed, title); got != 1 {
		t.Errorf("feed has %d copies, want 1", got)
	}
}

func TestDeletingANotificationOnlyRemovesOneFeed(t *testing.T) {
	st := isolatedNotificationStore(t)
	ctx := context.Background()

	first := noticeUser(t, st, auth.RoleSubscriber)
	second := noticeUser(t, st, auth.RoleSubscriber)
	title := uniqueName("zz-notice-delete")
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `delete from notifications where title = $1`, title)
	})

	if _, err := st.CreateNotification(ctx, NotificationInput{
		Audience:   AudienceSubscribers,
		Recipients: []int64{first.ID, second.ID},
		Title:      title,
		Body:       "This notice belongs to two feeds.",
	}); err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}

	feed, err := st.Notifications(ctx, first.ID, 100)
	if err != nil {
		t.Fatalf("Notifications: %v", err)
	}
	item := findNotification(t, feed, title)
	if item == nil {
		t.Fatal("the notice is missing from the first feed")
	}

	if err := st.DeleteNotification(ctx, first.ID, item.ID); err != nil {
		t.Fatalf("DeleteNotification: %v", err)
	}

	feed, err = st.Notifications(ctx, first.ID, 100)
	if err != nil {
		t.Fatalf("Notifications after delete: %v", err)
	}
	if findNotification(t, feed, title) != nil {
		t.Error("the deleted notice is still in the first feed")
	}
	unread, err := st.UnreadNotifications(ctx, first.ID)
	if err != nil {
		t.Fatalf("UnreadNotifications: %v", err)
	}
	if unread != 0 {
		t.Errorf("unread = %d after deleting the only notice, want 0", unread)
	}

	feed, err = st.Notifications(ctx, second.ID, 100)
	if err != nil {
		t.Fatalf("Notifications for the other account: %v", err)
	}
	if findNotification(t, feed, title) == nil {
		t.Error("deleting one copy removed the other account's notice")
	}

	if err := st.DeleteNotification(ctx, first.ID, item.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting the same notice twice = %v, want ErrNotFound", err)
	}
}

func countNotifications(list []NotificationView, title string) int {
	count := 0
	for i := range list {
		if list[i].Title == title {
			count++
		}
	}
	return count
}

func TestNotificationLinksResolveToTheServiceAndAlert(t *testing.T) {
	st := isolatedNotificationStore(t)
	ctx := context.Background()

	subscriber := noticeUser(t, st, auth.RoleSubscriber)
	slug := uniqueName("zz-notice-link")
	serviceID := testService(t, st, slug)

	var alertID int64
	err := st.pool.QueryRow(ctx, `
		insert into alert_subscriptions (service_id, user_id)
		values ($1, $2)
		returning id`, serviceID, subscriber.ID).Scan(&alertID)
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}

	title := uniqueName("zz-notice-links")
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `delete from notifications where title = $1`, title)
	})

	if _, err := st.CreateNotification(ctx, NotificationInput{
		Audience:  AudienceSubscribers,
		Title:     title,
		Body:      "An alert delivery changed.",
		ServiceID: &serviceID,
		AlertID:   &alertID,
	}); err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}

	feed, err := st.Notifications(ctx, subscriber.ID, 100)
	if err != nil {
		t.Fatalf("Notifications: %v", err)
	}
	item := findNotification(t, feed, title)
	if item == nil {
		t.Fatal("the linked notice is missing from the feed")
	}
	if item.ServiceSlug != slug {
		t.Errorf("service slug = %q, want %q", item.ServiceSlug, slug)
	}
	if item.AlertID == nil || *item.AlertID != alertID {
		t.Errorf("alert id = %v, want %d", item.AlertID, alertID)
	}
	if item.AlertVendorSlug != slug {
		t.Errorf("alert vendor slug = %q, want %q", item.AlertVendorSlug, slug)
	}
}

func TestRecordFailureReportsAChangeFromWorkingToFailed(t *testing.T) {
	st := isolatedNotificationStore(t)
	ctx := context.Background()
	id := testService(t, st, uniqueName("zz-notice-poll"))

	first, err := st.RecordFailure(ctx, id, PollFailure{HTTPStatus: 500, Error: "boom"})
	if err != nil {
		t.Fatalf("first RecordFailure: %v", err)
	}
	if !first {
		t.Error("the first failed poll was not reported as a change")
	}

	first, err = st.RecordFailure(ctx, id, PollFailure{HTTPStatus: 500, Error: "boom again"})
	if err != nil {
		t.Fatalf("second RecordFailure: %v", err)
	}
	if first {
		t.Error("a repeated failure was reported as a change")
	}

	if _, err := st.ApplyReport(ctx, id, testReport("notice-page")); err != nil {
		t.Fatalf("ApplyReport: %v", err)
	}
	first, err = st.RecordFailure(ctx, id, PollFailure{HTTPStatus: 500, Error: "boom after recovery"})
	if err != nil {
		t.Fatalf("RecordFailure after recovery: %v", err)
	}
	if !first {
		t.Error("the failure after a working poll was not reported as a change")
	}
}
