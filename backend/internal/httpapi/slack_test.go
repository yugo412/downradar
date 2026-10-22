package httpapi

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

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

// slackSpy is an incoming webhook the test runs. It records what it was sent and
// answers the way Slack does: the word "ok" for a message it took, and a reason
// for one it did not.
type slackSpy struct {
	mu     sync.Mutex
	bodies []string
	status int
	answer string
}

func (s *slackSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	s.mu.Lock()
	s.bodies = append(s.bodies, string(body))
	status := s.status
	answer := s.answer
	s.mu.Unlock()

	if status == 0 {
		status = http.StatusOK
	}
	if answer == "" {
		answer = "ok"
	}

	w.WriteHeader(status)
	_, _ = w.Write([]byte(answer))
}

// refused makes the next answers the ones Slack gives for a message it will not
// take.
func (s *slackSpy) refused(answer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answer = answer
}

// sent is the last message the channel would have shown.
func (s *slackSpy) sent(t *testing.T) string {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		t.Fatal("nothing was sent to the Slack channel")
	}
	return s.bodies[len(s.bodies)-1]
}

// slackRouter is the test router with a Slack channel the test runs. The channel
// is named by the URL a page would paste, so it is the request that points the
// sender at the test's own server.
func slackRouter(t *testing.T) (*store.Store, http.Handler, *slackSpy, string) {
	t.Helper()

	spy := &slackSpy{}
	server := httptest.NewServer(spy)
	t.Cleanup(server.Close)

	st, router, _ := testRouterWithOptions(t, nil)
	return st, router, spy, server.URL + "/services/T0/B0/X"
}

// savedWithSlack writes an alert for one subscriber with a Slack channel, and
// returns the token a message would carry.
func savedWithSlack(t *testing.T, st *store.Store, user *store.User, serviceID int64, webhookURL string) string {
	t.Helper()

	token := fmt.Sprintf("token-slack-%d", time.Now().UnixNano())
	if err := st.SubscribeAlert(context.Background(), serviceID, &user.ID, store.AlertChange{
		Slack:      &store.AlertSlack{URL: webhookURL},
		SlackToken: token,
		Targets:    store.WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })
	return token
}

// A Slack channel is a way for somebody with an account: a visitor who is not
// signed in has no channel to name, the same way they have no webhook.
func TestASlackChannelIsForSubscribers(t *testing.T) {
	st, router, _, webhook := slackRouter(t)
	slug := alertVendor(t, st)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		fmt.Sprintf(`{"slack":{"url":%q}}`, webhook), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a visitor with no account: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "subscribers") {
		t.Errorf("answer %q does not say who it is for", rec.Body.String())
	}
}

// A sentence is not an address, so it is refused at the door rather than saved as
// the place messages go.
func TestASlackWebhookMustBeAnAddress(t *testing.T) {
	st, router, _, _ := slackRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	for _, webhookURL := range []string{
		"hooks.slack.com/services/T0/B0/X",
		"the alerts channel",
		"ftp://hooks.slack.com/services/T0/B0/X",
	} {
		body := fmt.Sprintf(`{"slack":{"url":%q}}`, webhookURL)
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q = %d, want 400: %s", webhookURL, rec.Code, rec.Body.String())
		}
	}
}

// The URL is saved with the alert and read back with the rest, so the page can
// show it masked and keep it on a save of something else.
func TestSavingASlackChannelAndReadingItBack(t *testing.T) {
	st, router, _, webhookURL := slackRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		fmt.Sprintf(`{"slack":{"url":%q},"wholeService":true,"components":[]}`, webhookURL), cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving a channel = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	state := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if state.Code != http.StatusOK {
		t.Fatalf("reading the alert = %d, want 200: %s", state.Code, state.Body.String())
	}
	var body struct {
		Subscribed  bool   `json:"subscribed"`
		Slack       string `json:"slack"`
		SlackPaused bool   `json:"slackPaused"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Subscribed {
		t.Error("an alert with a channel on it reads as none")
	}
	if body.Slack != webhookURL {
		t.Errorf("slack = %q, want the URL that was saved", body.Slack)
	}
	if body.SlackPaused {
		t.Error("a channel that was just saved reads as paused")
	}

	// The list says how a row arrives, and a channel-only row has no address to
	// hand back in the email field.
	list := call(t, router, http.MethodGet, "/api/v1/auth/alerts", "", cookie)
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200: %s", list.Code, list.Body.String())
	}
	var rows struct {
		Alerts []struct {
			Email    string   `json:"email"`
			Channels []string `json:"channels"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode the list: %v", err)
	}
	found := false
	for _, row := range rows.Alerts {
		if strings.Contains(strings.Join(row.Channels, ","), "slack") {
			found = true
			if row.Email != "" {
				t.Errorf("email = %q, want nothing for a channel with no address", row.Email)
			}
		}
	}
	if !found {
		t.Error("the alert list does not say the row arrives by Slack")
	}
}

// A channel is tried before it is saved, with a made up change, and what Slack
// says is handed back as it is.
func TestTryingASlackChannelBeforeSavingIt(t *testing.T) {
	st, router, spy, webhook := slackRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/slack/test",
		fmt.Sprintf(`{"url":%q}`, webhook), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK {
		t.Errorf("answer = %+v, want the message accepted", body)
	}

	sent := spy.sent(t)
	for _, want := range []string{"test message", "Major outage", "Open status page"} {
		if !strings.Contains(sent, want) {
			t.Errorf("what was sent does not contain %q:\n%s", want, sent)
		}
	}
	if strings.Contains(sent, "unsubscribe") {
		t.Errorf("a test carries a way out of something that was never saved:\n%s", sent)
	}
}

// A channel that is already saved is tried with the URL it was saved with,
// because the page holds it masked and a save of something else sends nothing.
func TestASlackChannelIsTriedWithTheSavedURL(t *testing.T) {
	st, router, spy, webhook := slackRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	savedWithSlack(t, st, user, id, webhook)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/slack/test", `{}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the saved URL: %s", rec.Code, rec.Body.String())
	}
	if sent := spy.sent(t); !strings.Contains(sent, "test message") {
		t.Errorf("what was sent is not a test:\n%s", sent)
	}
}

// Slack answers a refusal with a 200 and the reason in the body, and that reason
// is what the page shows rather than a failure of our own.
func TestARefusedSlackIsSaidBack(t *testing.T) {
	st, router, spy, webhook := slackRouter(t)
	spy.refused("invalid_payload")
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/slack/test",
		fmt.Sprintf(`{"url":%q}`, webhook), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the answer: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.OK {
		t.Error("a refusal was reported as a message that arrived")
	}
	if !strings.Contains(body.Error, "invalid_payload") {
		t.Errorf("error = %q, want what Slack said", body.Error)
	}
}

// The channel has its own switch like the other ways, and the link a message
// carries says it would stop a Slack channel rather than an address.
func TestASlackChannelCanBePaused(t *testing.T) {
	st, router, _, webhook := slackRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	token := savedWithSlack(t, st, user, id, webhook)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/pause",
		`{"kind":"slack","paused":true}`, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pausing the channel = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	state := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	var body struct {
		SlackPaused bool `json:"slackPaused"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.SlackPaused {
		t.Error("the channel did not read back as paused")
	}

	link := call(t, router, http.MethodGet, "/api/v1/alerts/"+token, "", "")
	if link.Code != http.StatusOK {
		t.Fatalf("the link = %d, want 200: %s", link.Code, link.Body.String())
	}
	var page struct {
		Kind        string `json:"kind"`
		Destination string `json:"destination"`
	}
	if err := json.Unmarshal(link.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode the link: %v", err)
	}
	if page.Kind != store.ChannelSlack {
		t.Errorf("kind = %q, want slack", page.Kind)
	}
	if page.Destination != "your Slack channel" {
		t.Errorf("destination = %q, want the channel masked", page.Destination)
	}
}
