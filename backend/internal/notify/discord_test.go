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

// changedDiscord subscribes a Discord channel and moves the vendor from ok to
// major, leaving one message.
func changedDiscord(t *testing.T, st *store.Store, webhookURL string) string {
	t.Helper()
	ctx := context.Background()

	slug, err := st.CreateService(ctx, store.NewService{
		Name:                  fmt.Sprintf("Zz Discord Vendor %d", time.Now().UnixNano()),
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() {
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
		Discord:      &store.AlertDiscord{URL: webhookURL},
		DiscordToken: "token-" + slug,
		Targets:      store.WholeVendor,
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
	return slug
}

// discordPayloadSent reads a message the way Discord does.
type discordPayloadSent struct {
	Embeds []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Color       int    `json:"color"`
		Timestamp   string `json:"timestamp"`
		Footer      *struct {
			Text string `json:"text"`
		} `json:"footer"`
	} `json:"embeds"`
	Components []struct {
		Type       int `json:"type"`
		Components []struct {
			Type  int    `json:"type"`
			Style int    `json:"style"`
			Label string `json:"label"`
			URL   string `json:"url"`
		} `json:"components"`
	} `json:"components"`
}

func sentDiscord(t *testing.T, got *receiver) discordPayloadSent {
	t.Helper()

	call := got.last(t)
	if call.method != "POST" {
		t.Errorf("method = %q, want POST", call.method)
	}
	if contentType := call.headers.Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	var payload discordPayloadSent
	if err := json.Unmarshal([]byte(call.body), &payload); err != nil {
		t.Fatalf("decode what was sent: %v (%s)", err, call.body)
	}
	return payload
}

// A message is an embed, not the webhook body: the change in the title, the
// status in the description, and links out in the buttons.
func TestDrainPostsToDiscord(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 204)

	slug := changedDiscord(t, st, server.URL+"/api/webhooks/100/token")

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	payload := sentDiscord(t, got)
	if len(payload.Embeds) != 1 {
		t.Fatalf("embeds = %d, want one", len(payload.Embeds))
	}
	embed := payload.Embeds[0]
	if !strings.Contains(embed.Title, "Major outage") {
		t.Errorf("title = %q, want the status it moved to", embed.Title)
	}
	if !strings.Contains(embed.Description, "Operational") {
		t.Errorf("description = %q, want the status it came from", embed.Description)
	}
	if embed.Color != 0xe93f33 {
		t.Errorf("color = %#x, want the major outage colour", embed.Color)
	}
	if embed.URL != "https://downradar.info/"+slug {
		t.Errorf("url = %q, want the vendor page", embed.URL)
	}
	if embed.Footer == nil || embed.Footer.Text != "downradar.info" {
		t.Errorf("footer = %+v, want the site's name", embed.Footer)
	}
	if embed.Timestamp == "" {
		t.Error("the embed does not say when the change happened")
	}

	if len(payload.Components) != 1 || len(payload.Components[0].Components) != 2 {
		t.Fatalf("components = %+v, want two link buttons", payload.Components)
	}
	labels := []string{
		payload.Components[0].Components[0].Label,
		payload.Components[0].Components[1].Label,
	}
	if labels[0] != "Open status page" || labels[1] != "Stop these alerts" {
		t.Errorf("buttons = %v, want the page and the way out", labels)
	}

	body := got.last(t).body
	for _, want := range []string{
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

// A refusal leaves the delivery queued rather than counted as sent.
func TestDiscordRefusalIsRetried(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 400)
	got.answerWith(`{"message":"Invalid Form Body"}`)

	changedDiscord(t, st, server.URL+"/api/webhooks/100/token")

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
		if alert.DiscordURL == server.URL+"/api/webhooks/100/token" {
			found = true
		}
	}
	if !found {
		t.Error("a refused message is not queued any more")
	}
}

// A channel that has just been connected gets one message saying so, so the
// install is visible without waiting for a change.
func TestWelcomeDiscordSaysItIsConnected(t *testing.T) {
	server, got := newReceiver(t, 204)

	n := New(nil, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.WelcomeDiscord(context.Background(), server.URL+"/api/webhooks/100/token",
		"Zz Discord Vendor", "zz-discord-vendor"); err != nil {
		t.Fatalf("WelcomeDiscord: %v", err)
	}

	payload := sentDiscord(t, got)
	if len(payload.Embeds) != 1 {
		t.Fatalf("embeds = %d, want one", len(payload.Embeds))
	}
	if !strings.Contains(payload.Embeds[0].Title, "connected") {
		t.Errorf("title = %q, want it to say the channel is connected", payload.Embeds[0].Title)
	}

	body := got.last(t).body
	for _, want := range []string{
		"Zz Discord Vendor",
		"Open status page",
		"https://downradar.info/zz-discord-vendor",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the message does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Stop these alerts") {
		t.Errorf("a welcome message carries a way out of something that was never saved:\n%s", body)
	}
}

// A test says it is one and carries no way out; nothing is saved yet.
func TestTestDiscordSaysItIsATest(t *testing.T) {
	st := testStore(t)
	slug := changedDiscord(t, st, "https://discord.com/api/webhooks/100/token")

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	server, got := newReceiver(t, 204)

	result, err := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger()).
		TestDiscord(context.Background(), id, server.URL+"/api/webhooks/100/token")
	if err != nil {
		t.Fatalf("TestDiscord: %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %+v, want it accepted", result)
	}

	payload := sentDiscord(t, got)
	if len(payload.Embeds) != 1 || !strings.HasPrefix(payload.Embeds[0].Title, "Test: ") {
		t.Errorf("title = %q, want it to say it is a test", payload.Embeds[0].Title)
	}
	if !strings.Contains(got.last(t).body, "test message") {
		t.Errorf("the message does not say it is a test:\n%s", got.last(t).body)
	}
	if strings.Contains(got.last(t).body, "Stop these alerts") {
		t.Errorf("a test carries a way out of something that was never saved:\n%s", got.last(t).body)
	}
}
