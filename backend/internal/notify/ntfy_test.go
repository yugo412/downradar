package notify

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/store"
)

// changedNtfy writes a vendor, subscribes an ntfy topic to it, and moves it from
// ok to major, which is one notification waiting to go out.
func changedNtfy(t *testing.T, st *store.Store, topicURL, token string) (string, int64) {
	t.Helper()
	ctx := context.Background()

	slug, err := st.CreateService(ctx, store.NewService{
		Name:                  fmt.Sprintf("Zz ntfy Vendor %d", time.Now().UnixNano()),
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() {
		// Unsubscribing first drops anything still waiting, so a test never leaves
		// a notification for the running development stack to send.
		_ = st.UnsubscribeAlert(context.Background(), "token-"+slug)
		if _, err := st.DeleteService(context.Background(), slug); err != nil {
			t.Errorf("DeleteService: %v", err)
		}
	})

	id, name, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	if err := st.SubscribeAlert(ctx, id, nil, store.AlertChange{
		Ntfy:      &store.AlertNtfy{URL: topicURL, AccessToken: token},
		NtfyToken: "token-" + slug,
		Targets:   store.WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	report := func(status adapters.Health) adapters.Report {
		return adapters.Report{
			Global:    status,
			FetchedAt: time.Now().UTC().Add(-time.Hour),
			Components: []adapters.ComponentStatus{
				{SourceID: "c1", Name: name, Position: 1, Status: adapters.HealthOK},
			},
		}
	}
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	return slug, id
}

func TestDrainPublishesToNtfy(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 200)

	slug, _ := changedNtfy(t, st, server.URL+"/my-topic", "tk_test")

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	call := got.last(t)
	if call.method != "POST" {
		t.Errorf("method = %q, want POST", call.method)
	}
	if call.url != "/my-topic" {
		t.Errorf("path = %q, want /my-topic", call.url)
	}
	if auth := call.headers.Get("Authorization"); auth != "Bearer tk_test" {
		t.Errorf("Authorization = %q, want the topic token", auth)
	}
	if title := call.headers.Get("Title"); !strings.Contains(title, "Major outage") {
		t.Errorf("Title = %q, want the status it moved to", title)
	}
	if click := call.headers.Get("Click"); !strings.Contains(click, "/"+slug) {
		t.Errorf("Click = %q, want the vendor page", click)
	}
	if actions := call.headers.Get("Actions"); !strings.Contains(actions, "Stop alerts") {
		t.Errorf("Actions = %q, want a way to stop the alerts", actions)
	}
	if !strings.Contains(call.body, "From Operational to Major outage") {
		t.Errorf("body = %q, want the change", call.body)
	}
	if strings.Contains(call.body, "Open the status page") {
		t.Errorf("body = %q, want the link only on the action", call.body)
	}
	if !strings.Contains(call.body, "Stop these alerts:") {
		t.Errorf("body = %q, want the way out", call.body)
	}
}

func TestTestNtfySaysItIsATest(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 200)

	slug, serviceID := changedNtfy(t, st, server.URL+"/my-topic", "")

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if _, err := n.TestNtfy(context.Background(), serviceID, server.URL+"/my-topic", ""); err != nil {
		t.Fatalf("TestNtfy: %v", err)
	}

	call := got.last(t)
	if title := call.headers.Get("Title"); !strings.HasPrefix(title, "Test: ") {
		t.Errorf("Title = %q, want a test title", title)
	}
	if !strings.Contains(call.body, "This is a test message") {
		t.Errorf("body = %q, want it to say it is a test", call.body)
	}
	if strings.Contains(call.body, "Stop these alerts") {
		t.Errorf("body = %q, want no way out from a test", call.body)
	}
	if strings.Contains(call.headers.Get("Actions"), "Stop alerts") {
		t.Errorf("Actions = %q, want no stop button from a test", call.headers.Get("Actions"))
	}
	if !strings.Contains(call.headers.Get("Click"), "/"+slug) {
		t.Errorf("Click = %q, want the vendor page", call.headers.Get("Click"))
	}
}

func TestNtfyRefusalIsRetried(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 401)
	got.answerWith("invalid access token")

	_, _ = changedNtfy(t, st, server.URL+"/my-topic", "tk_wrong")

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	pending, err := st.PendingAlerts(context.Background(), maxAttempts, batchSize)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	found := false
	for _, alert := range pending {
		if alert.NtfyURL != server.URL+"/my-topic" {
			continue
		}
		found = true
		if alert.Attempts != 1 {
			t.Errorf("attempts = %d, want 1", alert.Attempts)
		}
	}
	if !found {
		t.Fatal("the refused notification was not left to retry")
	}
}
