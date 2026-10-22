package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/store"
)

// receiver is a small endpoint that records what it was called with, so a test
// can look at a call the way the far end would.
type receiver struct {
	mu    sync.Mutex
	calls []receivedCall
	reply int
	body  string
}

type receivedCall struct {
	method  string
	url     string
	headers http.Header
	body    string
}

func newReceiver(t *testing.T, reply int) (*httptest.Server, *receiver) {
	t.Helper()

	got := &receiver{reply: reply}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.mu.Lock()
		got.calls = append(got.calls, receivedCall{
			method:  r.Method,
			url:     r.URL.String(),
			headers: r.Header.Clone(),
			body:    string(body),
		})
		answer := got.body
		reply := got.reply
		got.mu.Unlock()
		w.WriteHeader(reply)
		if answer != "" {
			_, _ = w.Write([]byte(answer))
		}
	}))
	t.Cleanup(server.Close)
	return server, got
}

// answerWith makes the receiver reply with something to read, which is what a
// subscriber looks at when they try a webhook from the page.
func (r *receiver) answerWith(body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.body = body
}

// answerStatus makes the receiver answer differently from now on, which is how a
// test gets a far end that stops working halfway through.
func (r *receiver) answerStatus(reply int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reply = reply
}

func (r *receiver) last(t *testing.T) receivedCall {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		t.Fatal("the webhook was never called")
	}
	return r.calls[len(r.calls)-1]
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// receivedPayload is a call's body read the way a subscriber's code would read
// it, so a test says what the far end can act on rather than what the bytes are.
type receivedPayload struct {
	Service struct {
		Name   string `json:"name"`
		Slug   string `json:"slug"`
		URL    string `json:"url"`
		Status string `json:"status"`
	} `json:"service"`
	Changed struct {
		Component string `json:"component"`
		From      string `json:"from"`
		To        string `json:"to"`
		At        string `json:"at"`
		Text      string `json:"text"`
	} `json:"changed"`
	Components []struct {
		Name   string `json:"name"`
		Group  string `json:"group"`
		Status string `json:"status"`
	} `json:"components"`
}

func decodePayload(t *testing.T, body string) receivedPayload {
	t.Helper()

	var payload receivedPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("body = %q, want JSON: %v", body, err)
	}
	return payload
}

// webhookVendor subscribes one webhook and moves the vendor from ok to major,
// leaving one call. The owner is whoever the call is recorded for, or nobody.
func webhookVendor(t *testing.T, st *store.Store, userID *int64, hook store.AlertWebhook) string {
	t.Helper()
	ctx := context.Background()

	slug, err := st.CreateService(ctx, store.NewService{
		Name:                  fmt.Sprintf("Zz Hook Vendor %d", time.Now().UnixNano()),
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() {
		// Unsubscribing first drops the call still waiting, so a test never leaves
		// one for the running development stack to make.
		_ = st.UnsubscribeAlert(context.Background(), "token-"+slug)
		if _, err := st.DeleteService(context.Background(), slug); err != nil {
			t.Errorf("DeleteService: %v", err)
		}
	})

	id, name, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	if err := st.SubscribeAlert(ctx, id, userID, store.AlertChange{
		Webhook: &hook, WebhookToken: "token-" + slug, Targets: store.WholeVendor,
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

// A call uses the method, URL and headers the subscriber wrote, with placeholders
// filled in and the channel's token attached.
func TestDrainCallsTheWebhookTheSubscriberDescribed(t *testing.T) {
	st := testStore(t)

	server, calls := newReceiver(t, http.StatusNoContent)
	slug := webhookVendor(t, st, nil, store.AlertWebhook{
		URL:    server.URL + "/hook",
		Method: http.MethodPost,
		Headers: []store.AlertField{
			{Name: "X-Source", Value: "downradar"},
			{Name: "X-Vendor", Value: "{{slug}}"},
		},
	})

	// The fake sender takes the emails; a call never goes near a mail server.
	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	got := calls.last(t)
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.url != "/hook" {
		t.Errorf("url = %q, want the one it was saved with", got.url)
	}
	// Nothing is added to the headers the subscriber wrote: a webhook that wants
	// an Authorization header writes one.
	if got := got.headers.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want no header the subscriber did not write", got)
	}
	if got.headers.Get("X-Source") != "downradar" {
		t.Errorf("X-Source = %q, want the header the subscriber wrote", got.headers.Get("X-Source"))
	}
	if got.headers.Get("X-Vendor") != slug {
		t.Errorf("X-Vendor = %q, want the placeholder filled in", got.headers.Get("X-Vendor"))
	}
	if want := "application/json"; got.headers.Get("Content-Type") != want {
		t.Errorf("Content-Type = %q, want %q", got.headers.Get("Content-Type"), want)
	}

	payload := decodePayload(t, got.body)
	if payload.Service.Slug != slug {
		t.Errorf("service.slug = %q, want %q", payload.Service.Slug, slug)
	}
	if want := "https://downradar.info/" + slug; payload.Service.URL != want {
		t.Errorf("service.url = %q, want %q", payload.Service.URL, want)
	}
	if payload.Service.Status != "major" {
		t.Errorf("service.status = %q, want the vendor as it stands now", payload.Service.Status)
	}
	if payload.Changed.Component != "" {
		t.Errorf("changed.component = %q, want none when the vendor's own status moved", payload.Changed.Component)
	}
	if payload.Changed.From != "ok" || payload.Changed.To != "major" {
		t.Errorf("changed = %q to %q, want the move that earned the call", payload.Changed.From, payload.Changed.To)
	}
	if _, err := time.Parse(time.RFC3339, payload.Changed.At); err != nil {
		t.Errorf("changed.at = %q, want the moment of the change: %v", payload.Changed.At, err)
	}
	if !strings.Contains(payload.Changed.Text, "Major outage") {
		t.Errorf("changed.text = %q, want a line a person can read", payload.Changed.Text)
	}
	if len(payload.Components) != 1 || payload.Components[0].Status != "ok" {
		t.Errorf("components = %+v, want the vendor's own list as it stands", payload.Components)
	}

	// A call that was answered is done: the next pass has nothing to do.
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if got := calls.count(); got != 1 {
		t.Errorf("calls after a second pass = %d, want 1", got)
	}
}

// A component nobody picked is still a call, because what a webhook carries is
// the vendor's whole state rather than a pick of it: the email is the way that
// reports on what was asked for, and it is the only way that does.
func TestADrainCallsTheWebhookWhenAComponentMoves(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	server, calls := newReceiver(t, http.StatusOK)
	slug := webhookVendor(t, st, nil, store.AlertWebhook{URL: server.URL + "/hook", Method: http.MethodPost})

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(ctx); err != nil {
		t.Fatalf("first drain: %v", err)
	}

	// The component moves and the vendor's own status stays where it was.
	id, name, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, adapters.Report{
		Global:    adapters.HealthMajor,
		FetchedAt: time.Now().UTC().Add(-time.Hour),
		Components: []adapters.ComponentStatus{
			{SourceID: "c1", Name: name, Position: 1, Status: adapters.HealthDegraded},
		},
	}); err != nil {
		t.Fatalf("component poll: %v", err)
	}
	if err := n.drain(ctx); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	if got := calls.count(); got != 2 {
		t.Fatalf("calls = %d, want one per change", got)
	}
	payload := decodePayload(t, calls.last(t).body)
	if payload.Changed.Component != name {
		t.Errorf("changed.component = %q, want %q", payload.Changed.Component, name)
	}
	if payload.Changed.From != "ok" || payload.Changed.To != "degraded" {
		t.Errorf("changed = %q to %q, want the component's move", payload.Changed.From, payload.Changed.To)
	}
	if len(payload.Components) != 1 || payload.Components[0].Status != "degraded" {
		t.Errorf("components = %+v, want the vendor as it stands now", payload.Components)
	}
}

// A receiver that refuses a call has refused it, and one that answers nothing at
// all is a failure too: the loop tries a few times and then leaves the delivery
// with its error rather than calling for ever.
func TestDrainGivesUpOnAWebhookThatKeepsRefusing(t *testing.T) {
	st := testStore(t)

	server, calls := newReceiver(t, http.StatusInternalServerError)
	slug := webhookVendor(t, st, nil, store.AlertWebhook{
		URL:    server.URL,
		Method: http.MethodPost,
	})

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	for i := 0; i < maxAttempts; i++ {
		if err := n.drain(context.Background()); err != nil {
			t.Fatalf("drain %d: %v", i+1, err)
		}
	}

	if got := calls.count(); got != maxAttempts {
		t.Errorf("calls = %d, want one per attempt", got)
	}

	pending, err := st.PendingAlerts(context.Background(), maxAttempts, 100)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	for _, alert := range pending {
		if alert.Token == "token-"+slug {
			t.Errorf("a call that used up its attempts is still queued: %+v", alert)
		}
	}

	// One more pass must not call again.
	if err := n.drain(context.Background()); err != nil {
		t.Fatalf("final drain: %v", err)
	}
	if got := calls.count(); got != maxAttempts {
		t.Errorf("calls after a final pass = %d, want %d", got, maxAttempts)
	}
}

// What lands in a body is put there by the encoder, so a vendor name with a
// quote in it cannot end the string it sits in, and a vendor with nothing under
// it carries an empty list rather than a null a receiver has to know about.
func TestTheWebhookBodyEscapesWhatItCarries(t *testing.T) {
	quoted := `Zz "Quoted" Vendor`
	body, err := webhookBody(
		store.PendingAlert{VendorName: quoted, FromStatus: "ok", ToStatus: "major"},
		webhookData{At: "2026-10-19T04:45:00Z", Text: quoted + " is now Major outage"},
		store.ServiceSnapshot{Status: "major"})
	if err != nil {
		t.Fatalf("webhookBody: %v", err)
	}
	if !strings.Contains(body, `"components":[]`) {
		t.Errorf("body = %q, want an empty list of components", body)
	}

	payload := decodePayload(t, body)
	if payload.Service.Name != quoted {
		t.Errorf("service.name = %q, want %q", payload.Service.Name, quoted)
	}
	if payload.Changed.Text != quoted+" is now Major outage" {
		t.Errorf("changed.text = %q, want the sentence with the quotes in it", payload.Changed.Text)
	}
}

// A header cannot hold a line break, so one that does is refused rather than
// sent: that would be a way to write a header the call did not ask for.
func TestRenderHeadersRefusesALineBreak(t *testing.T) {
	if _, err := renderHeaders(
		[]store.AlertField{{Name: "X-Bad", Value: "one\ntwo"}}, webhookData{}); err == nil {
		t.Error("a header with a line break was rendered as if it were fine")
	}
}

// A placeholder this build does not know is left as it was written rather than
// sent as nothing, so a typo shows up in what the far end receives.
func TestRenderHeadersLeaveUnknownPlaceholdersAlone(t *testing.T) {
	headers, err := renderHeaders(
		[]store.AlertField{{Name: "X-Test", Value: "{{nope}} and {{to}}"}}, webhookData{To: "Degraded"})
	if err != nil {
		t.Fatalf("renderHeaders: %v", err)
	}
	if len(headers) != 1 || headers[0].Value != "{{nope}} and Degraded" {
		t.Errorf("headers = %+v, want the unknown name left as written", headers)
	}
}

// hookOwner writes the account a call is recorded for. A webhook hangs off an
// account rather than off an address, which is why the log has somewhere to be
// shown.
func hookOwner(t *testing.T, st *store.Store) *store.User {
	t.Helper()

	email := fmt.Sprintf("zz-hook-log-%d@example.org", time.Now().UnixNano())
	user, err := st.CreateUser(context.Background(), email, "Hook Owner", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteUser(context.Background(), user.ID) })
	return user
}

// snapshotVendor writes a vendor with one component under it and nothing
// subscribed to it, which is what a try from the page is made against: the page
// has the vendor on screen before anything about it is saved.
func snapshotVendor(t *testing.T, st *store.Store) (int64, string, string) {
	t.Helper()
	ctx := context.Background()

	component := fmt.Sprintf("Zz Component %d", time.Now().UnixNano())
	slug, err := st.CreateService(ctx, store.NewService{
		Name:                  fmt.Sprintf("Zz Try Vendor %d", time.Now().UnixNano()),
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() {
		if _, err := st.DeleteService(context.Background(), slug); err != nil {
			t.Errorf("DeleteService: %v", err)
		}
	})

	id, _, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, adapters.Report{
		Global:    adapters.HealthOK,
		FetchedAt: time.Now().UTC().Add(-time.Hour),
		Components: []adapters.ComponentStatus{
			{SourceID: "c1", Name: component, Position: 1, Status: adapters.HealthOK},
		},
	}); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	return id, slug, component
}

// What a call did is written down where the subscriber can see it, so a webhook
// that has quietly stopped working is something they can find out rather than
// hear nothing about.
func TestADrainWritesDownTheCallItMade(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	user := hookOwner(t, st)
	server, calls := newReceiver(t, http.StatusAccepted)
	calls.answerWith(`{"queued":true}`)
	slug := webhookVendor(t, st, &user.ID, store.AlertWebhook{
		URL: server.URL + "/hook", Method: http.MethodPost,
	})

	serviceID, _, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	if err := n.drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	logged, err := st.WebhookCalls(ctx, user.ID, serviceID, 10)
	if err != nil {
		t.Fatalf("WebhookCalls: %v", err)
	}
	if len(logged) != 1 {
		t.Fatalf("calls written down = %d, want the one that went out", len(logged))
	}
	got := logged[0]
	if got.Kind != store.CallDelivery || got.Method != http.MethodPost {
		t.Errorf("call = %+v, want the delivery it was", got)
	}
	if got.URL != server.URL+"/hook" {
		t.Errorf("call url = %q, want where it was sent", got.URL)
	}
	if got.Status == nil || *got.Status != http.StatusAccepted {
		t.Errorf("call status = %v, want what the far end answered", got.Status)
	}
	if got.Response != `{"queued":true}` {
		t.Errorf("call response = %q, want what it answered with", got.Response)
	}
	if got.Error != "" {
		t.Errorf("call error = %q, want none for a call that was answered", got.Error)
	}

	// A far end that starts refusing is written down with what it said, which is
	// the answer to "why is nothing arriving".
	calls.answerStatus(http.StatusBadGateway)
	id, name, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, adapters.Report{
		Global:    adapters.HealthDegraded,
		FetchedAt: time.Now().UTC().Add(-time.Hour),
		Components: []adapters.ComponentStatus{
			{SourceID: "c1", Name: name, Position: 1, Status: adapters.HealthOK},
		},
	}); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if err := n.drain(ctx); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	logged, err = st.WebhookCalls(ctx, user.ID, serviceID, 10)
	if err != nil {
		t.Fatalf("WebhookCalls: %v", err)
	}
	got = logged[0]
	if got.Status == nil || *got.Status != http.StatusBadGateway {
		t.Errorf("call status = %v, want what the refusing far end answered", got.Status)
	}
	if !strings.Contains(got.Error, "answered") {
		t.Errorf("call error = %q, want it to say the far end answered", got.Error)
	}
}

// A try is a real call with a made-up change, and a refusal comes back as the
// answer rather than a mistake on this side.
func TestATestCallCarriesAnExampleChange(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	server, calls := newReceiver(t, http.StatusOK)
	calls.answerWith("thanks")
	id, slug, component := snapshotVendor(t, st)

	n := New(st, &fakeSender{}, "https://downradar.info", time.Second, quietLogger())
	result, err := n.TestWebhook(ctx, id, store.AlertWebhook{
		URL:     server.URL + "/hook",
		Method:  http.MethodPut,
		Headers: []store.AlertField{{Name: "X-Component", Value: "{{component}}"}},
	})
	if err != nil {
		t.Fatalf("TestWebhook: %v", err)
	}
	if result.Status != http.StatusOK || result.Response != "thanks" {
		t.Errorf("result = %+v, want what the far end answered", result)
	}
	if result.Error != "" {
		t.Errorf("result error = %q, want none for a call that was answered", result.Error)
	}

	got := calls.last(t)
	if got.method != http.MethodPut {
		t.Errorf("method = %q, want the one the page was holding", got.method)
	}
	if got.headers.Get("X-Component") != component {
		t.Errorf("X-Component = %q, want %q from the change the call makes up",
			got.headers.Get("X-Component"), component)
	}
	if got.headers.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want the body to say what it is", got.headers.Get("Content-Type"))
	}

	payload := decodePayload(t, got.body)
	if payload.Service.Slug != slug || payload.Service.Name == "" {
		t.Errorf("service = %+v, want the vendor the page is about", payload.Service)
	}
	if payload.Changed.Component != component {
		t.Errorf("changed.component = %q, want %q", payload.Changed.Component, component)
	}
	if payload.Changed.From != "ok" || payload.Changed.To != "major" {
		t.Errorf("changed = %q to %q, want the example the page shows", payload.Changed.From, payload.Changed.To)
	}
	if len(payload.Components) != 1 || payload.Components[0].Name != component {
		t.Errorf("components = %+v, want the vendor's own list", payload.Components)
	}

	// A far end that refuses comes back with what it said.
	refusing, _ := newReceiver(t, http.StatusUnauthorized)
	result, err = n.TestWebhook(ctx, id, store.AlertWebhook{URL: refusing.URL, Method: http.MethodPost})
	if err == nil {
		t.Error("a refused call came back as if it had been accepted")
	}
	if result.Status != http.StatusUnauthorized || !strings.Contains(result.Error, "401") {
		t.Errorf("result = %+v, want the status and the line the far end answered", result)
	}

	// One that cannot be reached says so rather than being reported as an answer.
	result, err = n.TestWebhook(ctx, id, store.AlertWebhook{
		URL: "https://nobody.invalid/hook", Method: http.MethodPost,
	})
	if err == nil || result.Status != 0 || result.Error == "" {
		t.Errorf("result = %+v with error %v, want a call that never got there", result, err)
	}
}
