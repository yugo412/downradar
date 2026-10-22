package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/store"
)

// fakeSender records what would have gone out, and can be told to fail.
type fakeSender struct {
	mu       sync.Mutex
	messages []sentMessage
	fail     error
}

type sentMessage struct {
	to      string
	subject string
	body    string
}

func (f *fakeSender) Send(ctx context.Context, to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.messages = append(f.messages, sentMessage{to: to, subject: subject, body: body})
	return nil
}

func (f *fakeSender) sent() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMessage(nil), f.messages...)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testStore(t *testing.T) *store.Store {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set, skipping the integration test")
	}

	ctx := context.Background()
	st, err := store.New(ctx, dsn, store.Options{AppKey: "test-app-key"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// changedVendor writes a vendor, subscribes an address to it, and moves it from
// ok to major, which is one alert waiting to go out.
func changedVendor(t *testing.T, st *store.Store, email string) string {
	t.Helper()
	ctx := context.Background()

	slug, err := st.CreateService(ctx, store.NewService{
		Name:                  fmt.Sprintf("Zz Notify Vendor %d", time.Now().UnixNano()),
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() {
		// Unsubscribing first drops anything still waiting, so a test never
		// leaves a delivery for the running development stack to send.
		_ = st.UnsubscribeAlert(context.Background(), "token-"+slug)
		if _, err := st.DeleteService(context.Background(), slug); err != nil {
			t.Errorf("DeleteService: %v", err)
		}
	})

	id, name, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	if err := st.SubscribeAlert(ctx, id, nil, store.EmailOnly(email, "token-"+slug, store.WholeVendor)); err != nil {
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

// changedComponent writes a vendor, subscribes an address to one of its
// components, and moves that component without moving the vendor's own status,
// which is one alert waiting to go out about a component.
func changedComponent(t *testing.T, st *store.Store, email string) (string, string) {
	t.Helper()
	ctx := context.Background()

	slug, err := st.CreateService(ctx, store.NewService{
		Name:                  fmt.Sprintf("Zz Notify Component %d", time.Now().UnixNano()),
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() {
		// Unsubscribing first drops anything still waiting, so a test never
		// leaves a delivery for the running development stack to send.
		_ = st.UnsubscribeAlert(context.Background(), "token-"+slug)
		if _, err := st.DeleteService(context.Background(), slug); err != nil {
			t.Errorf("DeleteService: %v", err)
		}
	})

	id, name, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	report := func(global, component adapters.Health) adapters.Report {
		return adapters.Report{
			Global:    global,
			FetchedAt: time.Now().UTC(),
			Components: []adapters.ComponentStatus{
				{SourceID: "c1", Name: name + " Search", Position: 1, Status: component},
			},
		}
	}

	// The first poll is the baseline a change is read against, and the poll that
	// gives the component a row for a target to point at.
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthOK, adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}

	componentIDs, err := st.ComponentIDs(ctx, id, []string{"c1"})
	if err != nil {
		t.Fatalf("ComponentIDs: %v", err)
	}
	if err := st.SubscribeAlert(ctx, id, nil, store.EmailOnly(email, "token-"+slug,
		store.AlertTargets{Components: componentIDs})); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthOK, adapters.HealthDegraded)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	return slug, name + " Search"
}

func TestDrainSendsWhatChanged(t *testing.T) {
	st := testStore(t)
	slug := changedVendor(t, st, "ann@example.com")

	sender := &fakeSender{}
	n := New(st, sender, "https://downradar.info/", time.Second, quietLogger())

	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	sent := sender.sent()
	if len(sent) != 1 {
		t.Fatalf("sent = %d messages, want 1", len(sent))
	}
	if sent[0].to != "ann@example.com" {
		t.Errorf("sent to %q, want the subscriber", sent[0].to)
	}
	if !strings.Contains(sent[0].subject, "Major outage") {
		t.Errorf("subject = %q, want the wording the site uses", sent[0].subject)
	}

	body := sent[0].body
	for _, want := range []string{
		"Operational",  // where it was
		"Major outage", // where it went
		"https://downradar.info/" + slug,
		"https://downradar.info/alerts/unsubscribe?token=token-" + slug,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}
	// The reader is told what the email is about, in the words the site uses.
	if !strings.Contains(body, "changes status") {
		t.Errorf("body does not say what it is about:\n%s", body)
	}

	// A delivered alert is done: the next pass has nothing to do.
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if got := len(sender.sent()); got != 1 {
		t.Errorf("sent after a second pass = %d, want 1", got)
	}
}

// An alert about a component says which one, because the reader is being told
// about one part of a page rather than about the vendor as a whole.
func TestDrainSaysWhichComponentMoved(t *testing.T) {
	st := testStore(t)
	_, component := changedComponent(t, st, "bob@example.com")

	sender := &fakeSender{}
	n := New(st, sender, "https://downradar.info/", time.Second, quietLogger())

	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	sent := sender.sent()
	if len(sent) != 1 {
		t.Fatalf("sent = %d messages, want the one about the component", len(sent))
	}
	if !strings.Contains(sent[0].subject, component) {
		t.Errorf("subject = %q, want it to name %s", sent[0].subject, component)
	}
	body := sent[0].body
	for _, want := range []string{
		component,                    // the heading names it
		"Operational",                // where it was
		"Degraded",                   // where it went
		"when " + component + " on ", // and the closing line says what was asked for
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q:\n%s", want, body)
		}
	}
}

func TestDrainGivesUpOnAnAddressThatKeepsFailing(t *testing.T) {
	st := testStore(t)
	changedVendor(t, st, "bob@example.com")

	sender := &fakeSender{fail: errors.New("mailbox unavailable")}
	n := New(st, sender, "https://downradar.info", time.Second, quietLogger())

	for i := 0; i < maxAttempts; i++ {
		if err := n.drain(context.Background()); err != nil {
			t.Fatalf("drain %d: %v", i+1, err)
		}
	}

	// Every attempt is counted, and the row stops being offered once it has used
	// them up, so a dead address cannot be retried for ever.
	pending, err := st.PendingAlerts(context.Background(), maxAttempts, 10)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	for _, alert := range pending {
		if alert.Email == "bob@example.com" {
			t.Errorf("a delivery that used up its attempts is still queued: %+v", alert)
		}
	}

	// One more pass must not send anything either.
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("final drain: %v", err)
	}
	if len(sender.sent()) != 0 {
		t.Errorf("sent = %d messages, want none", len(sender.sent()))
	}
}
