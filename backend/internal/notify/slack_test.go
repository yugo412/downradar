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

// changedSlack writes a vendor, subscribes a Slack channel to it, and moves it
// from ok to major, which is one message waiting to go out.
func changedSlack(t *testing.T, st *store.Store, webhookURL string) string {
	t.Helper()
	ctx := context.Background()

	slug, err := st.CreateService(ctx, store.NewService{
		Name:                  fmt.Sprintf("Zz Slack Vendor %d", time.Now().UnixNano()),
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

	if err := st.SubscribeAlert(ctx, id, nil, store.AlertChange{
		Slack:      &store.AlertSlack{URL: webhookURL},
		SlackToken: "token-" + slug,
		Targets:    store.WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	report := func(status adapters.Health) adapters.Report {
		return adapters.Report{
			Global:    status,
			FetchedAt: time.Now().UTC(),
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
	return slug
}

// slackPayloadSent is a message read the way Slack reads it, so a test says what
// the channel would show rather than what the bytes are.
type slackPayloadSent struct {
	Text   string `json:"text"`
	Blocks []struct {
		Type string `json:"type"`
		Text *struct {
			Text string `json:"text"`
		} `json:"text"`
		Elements []struct {
			Type string `json:"type"`
			Text any    `json:"text"`
		} `json:"elements"`
	} `json:"blocks"`
}

func sentSlack(t *testing.T, got *receiver) slackPayloadSent {
	t.Helper()

	call := got.last(t)
	if call.method != "POST" {
		t.Errorf("method = %q, want POST", call.method)
	}
	if contentType := call.headers.Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	var payload slackPayloadSent
	if err := json.Unmarshal([]byte(call.body), &payload); err != nil {
		t.Fatalf("decode what was sent: %v (%s)", err, call.body)
	}
	return payload
}

// A Slack message is a set of blocks rather than the webhook body: the change in
// a heading, the status it moved from and to, the page, and the way out.
func TestDrainPostsToSlack(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 200)
	got.answerWith("ok")

	slug := changedSlack(t, st, server.URL+"/services/T000/B000/XXXX")

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	payload := sentSlack(t, got)
	if !strings.Contains(payload.Text, "Major outage") {
		t.Errorf("text = %q, want the change in a sentence", payload.Text)
	}
	if len(payload.Blocks) == 0 {
		t.Fatal("the message carries no blocks")
	}

	// The heading is the first section, and it says the same thing the fallback
	// text does, because a phone shows one and the channel shows the other.
	if payload.Blocks[0].Type != "section" || payload.Blocks[0].Text == nil {
		t.Fatalf("first block = %+v, want a section with text", payload.Blocks[0])
	}
	if text := payload.Blocks[0].Text.Text; !strings.Contains(text, "Major outage") {
		t.Errorf("heading = %q, want the status it moved to", text)
	}

	body := got.last(t).body
	for _, want := range []string{
		"Operational",
		"https://downradar.info/" + slug,
		"Stop these alerts",
		"token=token-" + slug,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("message does not contain %q:\n%s", want, body)
		}
	}

	// A message that was accepted is done: the next pass has nothing to do.
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if got := got.count(); got != 1 {
		t.Errorf("calls after a second pass = %d, want 1", got)
	}
}

// Slack answers an incoming webhook with a 200 even when it refused the message,
// so the word in the body is what decides. A refusal leaves the delivery queued
// to be tried again rather than counted as sent.
func TestSlackRefusalIsRetried(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 200)
	got.answerWith("invalid_payload")

	changedSlack(t, st, server.URL+"/services/T000/B000/XXXX")

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	if calls := got.count(); calls != 2 {
		t.Errorf("calls = %d, want the refusal to be tried again", calls)
	}

	pending, err := st.PendingAlerts(context.Background(), maxAttempts, 10)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	found := false
	for _, alert := range pending {
		if alert.SlackURL == server.URL+"/services/T000/B000/XXXX" {
			found = true
		}
	}
	if !found {
		t.Error("a refused message is not queued any more")
	}
}

// A message tried from the page says that it is one and carries no way out:
// nothing has been saved yet for a link to stop.
func TestTestSlackSaysItIsATest(t *testing.T) {
	st := testStore(t)
	slug := changedSlack(t, st, "https://hooks.slack.com/services/T000/B000/XXXX")

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	server, got := newReceiver(t, 200)
	got.answerWith("ok")

	result, err := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger()).
		TestSlack(context.Background(), id, server.URL+"/services/T000/B000/XXXX")
	if err != nil {
		t.Fatalf("TestSlack: %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %+v, want it accepted", result)
	}

	payload := sentSlack(t, got)
	if !strings.HasPrefix(payload.Text, "Test: ") {
		t.Errorf("text = %q, want it to say it is a test", payload.Text)
	}
	if !strings.Contains(got.last(t).body, "test message") {
		t.Errorf("the message does not say it is a test:\n%s", got.last(t).body)
	}
	if strings.Contains(got.last(t).body, "unsubscribe") {
		t.Errorf("a test carries a way out of something that was never saved:\n%s", got.last(t).body)
	}
}

// A channel that has just been connected gets one message saying so, so the
// install is visible without waiting for a change.
func TestWelcomeSlackSaysItIsConnected(t *testing.T) {
	server, got := newReceiver(t, 200)
	got.answerWith("ok")

	n := New(nil, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.WelcomeSlack(context.Background(), server.URL+"/services/T000/B000/XXXX",
		"Zz Slack Vendor", "zz-slack-vendor"); err != nil {
		t.Fatalf("WelcomeSlack: %v", err)
	}

	payload := sentSlack(t, got)
	if !strings.Contains(payload.Text, "connected") {
		t.Errorf("text = %q, want it to say the channel is connected", payload.Text)
	}

	body := got.last(t).body
	for _, want := range []string{
		"Zz Slack Vendor",
		"Open status page",
		"https://downradar.info/zz-slack-vendor",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the message does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "unsubscribe") {
		t.Errorf("a welcome message carries a way out of something that was never saved:\n%s", body)
	}
}
