package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/store"
)

// changedChat writes a vendor, subscribes a chat to it, and moves it from ok to
// major, which is one message waiting to go out.
func changedChat(t *testing.T, st *store.Store, chatID string) string {
	t.Helper()
	ctx := context.Background()

	slug, err := st.CreateService(ctx, store.NewService{
		Name:                  fmt.Sprintf("Zz Telegram Vendor %d", time.Now().UnixNano()),
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
		// a message for the running development stack to send.
		_ = st.UnsubscribeAlert(context.Background(), "token-"+slug)
		if _, err := st.DeleteService(context.Background(), slug); err != nil {
			t.Errorf("DeleteService: %v", err)
		}
	})

	id, name, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	change := store.AlertChange{
		Telegram: &store.AlertTelegram{
			ChatID:   chatID,
			BotToken: "999:bot-secret",
		},
		TelegramToken: "token-" + slug,
		Targets:       store.WholeVendor,
	}
	if err := st.SubscribeAlert(ctx, id, nil, change); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	report := func(status adapters.Health) adapters.Report {
		return adapters.Report{
			Global:    status,
			FetchedAt: time.Now().UTC(),
			Components: []adapters.ComponentStatus{
				{SourceID: "c1", Name: name, Position: 1, Status: status},
			},
		}
	}
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	return slug
}

func telegramNotifier(t *testing.T, st *store.Store, api string) *Notifier {
	t.Helper()
	return New(st, &fakeSender{}, "https://downradar.info/", time.Second, quietLogger()).
		WithTelegram(api)
}

// sentTelegram is the text of the one message a receiver was sent, which is what
// a test looks at the way a chat would.
func sentTelegram(t *testing.T, got *receiver) (string, string) {
	t.Helper()

	got.mu.Lock()
	defer got.mu.Unlock()
	if len(got.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(got.calls))
	}

	var body struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(got.calls[0].body), &body); err != nil {
		t.Fatalf("decode what was sent: %v (%s)", err, got.calls[0].body)
	}
	return body.ChatID, body.Text
}

func TestDrainSendsTelegramMessages(t *testing.T) {
	st := testStore(t)
	slug := changedChat(t, st, "-1001234567890")

	server, got := newReceiver(t, 200)
	got.answerWith(`{"ok":true,"result":{"message_id":1}}`)

	if err := telegramNotifier(t, st, server.URL).drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	chatID, text := sentTelegram(t, got)
	if chatID != "-1001234567890" {
		t.Errorf("chat = %q, want the one the channel names", chatID)
	}
	for _, want := range []string{
		"Operational",  // where it was
		"Major outage", // where it went
		"https://downradar.info/" + slug,
		"Pause or stop these messages: https://downradar.info/alerts/unsubscribe?token=token-" + slug,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message does not contain %q:\n%s", want, text)
		}
	}
	if !strings.Contains(got.calls[0].url, "/bot999:bot-secret/sendMessage") {
		t.Errorf("called %q, want the bot's own sendMessage", got.calls[0].url)
	}
	if got.calls[0].method != "POST" {
		t.Errorf("method = %q, want POST", got.calls[0].method)
	}
}

// Telegram answers a chat it will not deliver to with a 200 and a body that says
// so, so what it says has to be what decides whether the message was accepted.
// A refusal leaves the delivery queued to be tried again rather than counted as
// sent.
func TestTelegramRefusalIsRetried(t *testing.T) {
	st := testStore(t)
	changedChat(t, st, "-1001234567890")

	server, got := newReceiver(t, 200)
	got.answerWith(`{"ok":false,"description":"Bad Request: chat not found"}`)

	n := telegramNotifier(t, st, server.URL)
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	got.mu.Lock()
	calls := len(got.calls)
	got.mu.Unlock()
	if calls != 2 {
		t.Errorf("calls = %d, want the refusal to be tried again", calls)
	}

	pending, err := st.PendingAlerts(context.Background(), maxAttempts, 10)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	found := false
	for _, alert := range pending {
		if alert.ChatID == "-1001234567890" {
			found = true
		}
	}
	if !found {
		t.Error("a refused message is not queued any more")
	}
}

// A message tried from the page says that it is one, and carries no way out:
// nothing has been saved yet for a link to stop.
func TestTestTelegramSaysItIsATest(t *testing.T) {
	st := testStore(t)
	slug := changedChat(t, st, "-1001234567890")

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	server, got := newReceiver(t, 200)
	got.answerWith(`{"ok":true,"result":{"message_id":1}}`)

	result, err := telegramNotifier(t, st, server.URL).TestTelegram(context.Background(), id, "999:bot-secret", "42")
	if err != nil {
		t.Fatalf("TestTelegram: %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %+v, want it accepted", result)
	}

	chatID, text := sentTelegram(t, got)
	if chatID != "42" {
		t.Errorf("chat = %q, want the one that was tried", chatID)
	}
	if !strings.Contains(text, "test message") {
		t.Errorf("message does not say it is a test:\n%s", text)
	}
	if strings.Contains(text, "unsubscribe") {
		t.Errorf("a test carries a way out of something that was never saved:\n%s", text)
	}
}
