package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

// isolatedNotificationRouter gives the notification tests a schema of their own.
// A broadcast legitimately reaches every subscriber, so another package running
// at the same time would otherwise be able to add rows to these feeds.
func isolatedNotificationRouter(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set, skipping the integration test")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	schema := fmt.Sprintf("zz_notice_http_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, `create schema `+schema); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `drop schema `+schema+` cascade`)
		_ = conn.Close(context.Background())
	})

	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	st, router, _ := testRouterWithDSN(t, dsn+separator+"search_path="+schema, nil)
	return st, router
}

type notificationFeed struct {
	Notifications []store.NotificationView `json:"notifications"`
	Count         int                      `json:"count"`
	Unread        int                      `json:"unread"`
}

func notificationTitle(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func TestTheNotificationFeedAndItsReadState(t *testing.T) {
	st, router := isolatedNotificationRouter(t)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)
	title := notificationTitle("zz-http-notice")

	if _, err := st.CreateNotification(t.Context(), store.NotificationInput{
		Audience: store.AudienceSubscribers,
		Title:    title,
		Body:     "Something the account should read.",
	}); err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}

	rec := call(t, router, http.MethodGet, "/api/v1/notifications", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed = %d, want 200: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var feed notificationFeed
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode feed: %v", err)
	}
	if feed.Count != 1 || len(feed.Notifications) != 1 {
		t.Fatalf("feed has %d items, want 1", len(feed.Notifications))
	}
	if feed.Unread != 1 {
		t.Errorf("unread = %d, want 1", feed.Unread)
	}
	item := feed.Notifications[0]
	if item.Title != title {
		t.Errorf("title = %q, want %q", item.Title, title)
	}
	if item.ReadAt != nil {
		t.Error("the notice arrived already read")
	}

	rec = call(t, router, http.MethodPatch,
		fmt.Sprintf("/api/v1/notifications/%d", item.ID), `{"read":true}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read = %d, want 200: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	rec = call(t, router, http.MethodGet, "/api/v1/notifications/unread", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("unread = %d, want 200: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var count struct {
		Unread int `json:"unread"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &count); err != nil {
		t.Fatalf("decode unread: %v", err)
	}
	if count.Unread != 0 {
		t.Errorf("unread = %d after marking read, want 0", count.Unread)
	}

	second := notificationTitle("zz-http-notice-second")
	if _, err := st.CreateNotification(t.Context(), store.NotificationInput{
		Audience: store.AudienceSubscribers,
		Title:    second,
		Body:     "One more for the feed.",
	}); err != nil {
		t.Fatalf("second CreateNotification: %v", err)
	}

	rec = call(t, router, http.MethodPost, "/api/v1/notifications/read", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark all = %d, want 200: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var marked struct {
		Updated int `json:"updated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &marked); err != nil {
		t.Fatalf("decode mark all: %v", err)
	}
	if marked.Updated != 1 {
		t.Errorf("updated = %d, want 1", marked.Updated)
	}

	rec = call(t, router, http.MethodGet, "/api/v1/notifications", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed after mark all = %d, want 200", rec.Code)
	}
	feed = notificationFeed{}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode feed after mark all: %v", err)
	}
	if feed.Unread != 0 {
		t.Errorf("unread = %d after mark all, want 0", feed.Unread)
	}
	for _, notice := range feed.Notifications {
		if notice.ReadAt == nil {
			t.Errorf("notice %d is still unread", notice.ID)
		}
	}
}

func TestOnlyStaffMayBroadcastAndSubscribersReceiveIt(t *testing.T) {
	st, router := isolatedNotificationRouter(t)

	admin := testUser(t, st, auth.RoleAdmin)
	subscriber := testUser(t, st, auth.RoleSubscriber)
	adminCookie := sessionFor(t, st, admin)
	subscriberCookie := sessionFor(t, st, subscriber)
	title := notificationTitle("zz-http-broadcast")
	body := fmt.Sprintf(`{"title":%q,"body":"A new feature is here."}`, title)

	if rec := call(t, router, http.MethodPost, "/api/v1/staff/notifications",
		body, subscriberCookie); rec.Code != http.StatusForbidden {
		t.Errorf("subscriber broadcast = %d, want 403", rec.Code)
	}

	if rec := call(t, router, http.MethodPost, "/api/v1/staff/notifications",
		`{"title":"","body":""}`, adminCookie); rec.Code != http.StatusBadRequest {
		t.Errorf("empty broadcast = %d, want 400", rec.Code)
	}

	rec := call(t, router, http.MethodPost, "/api/v1/staff/notifications", body, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("broadcast = %d, want 200: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var sent struct {
		Sent int `json:"sent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sent); err != nil {
		t.Fatalf("decode broadcast: %v", err)
	}
	if sent.Sent < 1 {
		t.Errorf("broadcast sent = %d, want at least the test subscriber", sent.Sent)
	}

	rec = call(t, router, http.MethodGet, "/api/v1/notifications", "", subscriberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscriber feed = %d, want 200", rec.Code)
	}
	var feed notificationFeed
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode subscriber feed: %v", err)
	}
	found := false
	for _, notice := range feed.Notifications {
		if notice.Title == title {
			found = true
		}
	}
	if !found {
		t.Error("the subscriber did not receive the broadcast")
	}

	rec = call(t, router, http.MethodGet, "/api/v1/notifications", "", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin feed = %d, want 200", rec.Code)
	}
	feed = notificationFeed{}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode admin feed: %v", err)
	}
	for _, notice := range feed.Notifications {
		if notice.Title == title {
			t.Error("staff received the subscriber broadcast")
		}
	}
}

func TestNotificationsNeedASession(t *testing.T) {
	_, router := isolatedNotificationRouter(t)

	for _, path := range []string{"/api/v1/notifications", "/api/v1/notifications/unread"} {
		if rec := call(t, router, http.MethodGet, path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", path, rec.Code)
		}
	}
}
