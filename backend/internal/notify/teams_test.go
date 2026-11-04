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

// changedTeams subscribes a Teams channel and moves the vendor from ok to major,
// leaving one card.
func changedTeams(t *testing.T, st *store.Store, webhookURL string) (string, int64) {
	t.Helper()
	ctx := context.Background()

	slug, err := st.CreateService(ctx, store.NewService{
		Name:                  fmt.Sprintf("Zz Teams Vendor %d", time.Now().UnixNano()),
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
		Teams:      &store.AlertTeams{URL: webhookURL},
		TeamsToken: "token-" + slug,
		Targets:    store.WholeVendor,
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

// teamsCardSent is the Adaptive Card the receiver was given.
type teamsCardSent struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Attachments []struct {
		ContentType string `json:"contentType"`
		Content     struct {
			Type    string `json:"type"`
			Version string `json:"version"`
			Body    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"body"`
			Actions []struct {
				Type  string `json:"type"`
				Title string `json:"title"`
				URL   string `json:"url"`
			} `json:"actions"`
		} `json:"content"`
	} `json:"attachments"`
}

func sentTeams(t *testing.T, got *receiver) teamsCardSent {
	t.Helper()

	call := got.last(t)
	if call.method != "POST" {
		t.Errorf("method = %q, want POST", call.method)
	}
	if contentType := call.headers.Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	var payload teamsCardSent
	if err := json.Unmarshal([]byte(call.body), &payload); err != nil {
		t.Fatalf("decode what was sent: %v (%s)", err, call.body)
	}
	return payload
}

// The card names the change, says where it came from, and offers the page and
// the way out as actions.
func TestDrainPostsToTeams(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 202)

	slug, _ := changedTeams(t, st, server.URL+"/workflows/one")

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	payload := sentTeams(t, got)
	if payload.Type != "message" || len(payload.Attachments) != 1 {
		t.Fatalf("payload = %+v, want one message attachment", payload)
	}
	if !strings.Contains(payload.Text, "Major outage") || !strings.Contains(payload.Text, "Operational") {
		t.Errorf("text = %q, want a plain fallback for a text workflow", payload.Text)
	}
	card := payload.Attachments[0]
	if card.ContentType != "application/vnd.microsoft.card.adaptive" || card.Content.Type != "AdaptiveCard" {
		t.Errorf("attachment = %+v, want an Adaptive Card", card)
	}

	var text strings.Builder
	for _, block := range card.Content.Body {
		if block.Text != "" {
			text.WriteString(block.Text)
			text.WriteString("\n")
		}
	}
	for _, want := range []string{"Major outage", "Operational"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("card text = %q, want it to mention %q", text.String(), want)
		}
	}

	if len(card.Content.Actions) != 2 {
		t.Fatalf("actions = %+v, want the page and the way out", card.Content.Actions)
	}
	if card.Content.Actions[0].Title != "Open status page" || card.Content.Actions[1].Title != "Stop these alerts" {
		t.Errorf("actions = %+v, want the page and the way out", card.Content.Actions)
	}
	if !strings.Contains(card.Content.Actions[0].URL, "/"+slug) {
		t.Errorf("page action = %q, want the vendor page", card.Content.Actions[0].URL)
	}
	if !strings.Contains(card.Content.Actions[1].URL, "token=token-"+slug) {
		t.Errorf("stop action = %q, want the alert token", card.Content.Actions[1].URL)
	}

	// A message that was accepted is done: the next pass has nothing to do.
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if got := got.count(); got != 1 {
		t.Errorf("calls after a second pass = %d, want 1", got)
	}
}

// A test says it is one and carries no way out.
func TestTestTeamsSaysItIsATest(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 202)

	slug, serviceID := changedTeams(t, st, server.URL+"/workflows/one")

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if _, err := n.TestTeams(context.Background(), serviceID, server.URL+"/workflows/one"); err != nil {
		t.Fatalf("TestTeams: %v", err)
	}

	card := sentTeams(t, got).Attachments[0].Content
	var text strings.Builder
	for _, block := range card.Body {
		text.WriteString(block.Text)
		text.WriteString("\n")
	}
	if !strings.Contains(text.String(), "This is a test message") {
		t.Errorf("card text = %q, want it to say it is a test", text.String())
	}
	if len(card.Actions) != 1 || card.Actions[0].Title != "Open status page" {
		t.Errorf("actions = %+v, want only the page from a test", card.Actions)
	}
	if strings.Contains(got.last(t).body, "token=token-"+slug) {
		t.Error("a test message carries the alert token")
	}
}

// A refusal leaves the delivery queued rather than counted as sent.
func TestTeamsRefusalIsRetried(t *testing.T) {
	st := testStore(t)
	server, got := newReceiver(t, 500)
	got.answerWith("workflow is turned off")

	_, _ = changedTeams(t, st, server.URL+"/workflows/one")

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
		if alert.TeamsURL != server.URL+"/workflows/one" {
			continue
		}
		found = true
		if alert.Attempts != 1 {
			t.Errorf("attempts = %d, want 1", alert.Attempts)
		}
	}
	if !found {
		t.Fatal("the refused card was not left to retry")
	}
}
