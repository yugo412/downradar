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

// discordSpy records what it was sent and answers the way Discord does.
type discordSpy struct {
	mu     sync.Mutex
	bodies []string
	status int
	answer string
}

func (s *discordSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	s.mu.Lock()
	s.bodies = append(s.bodies, string(body))
	status := s.status
	answer := s.answer
	s.mu.Unlock()

	if status == 0 {
		status = http.StatusNoContent
	}
	if answer != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(answer))
}

// refused makes the next answer a refusal.
func (s *discordSpy) refused(status int, answer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	s.answer = answer
}

// sent is the last message the channel received.
func (s *discordSpy) sent(t *testing.T) string {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		t.Fatal("nothing was sent to the Discord channel")
	}
	return s.bodies[len(s.bodies)-1]
}

// discordRouter is a test router whose Discord channel points at its own server.
func discordRouter(t *testing.T) (*store.Store, http.Handler, *discordSpy, string) {
	t.Helper()

	spy := &discordSpy{}
	server := httptest.NewServer(spy)
	t.Cleanup(server.Close)

	st, router, _ := testRouterWithOptions(t, nil)
	return st, router, spy, server.URL + "/api/webhooks/100/token"
}

// savedWithDiscord saves a subscriber's Discord channel and returns its token.
func savedWithDiscord(t *testing.T, st *store.Store, user *store.User, serviceID int64, webhookURL string) string {
	t.Helper()

	token := fmt.Sprintf("token-discord-%d", time.Now().UnixNano())
	if err := st.SubscribeAlert(context.Background(), serviceID, &user.ID, store.AlertChange{
		Discord:      &store.AlertDiscord{URL: webhookURL},
		DiscordToken: token,
		Targets:      store.WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })
	return token
}

// A Discord channel needs an account, like a webhook.
func TestADiscordChannelIsForSubscribers(t *testing.T) {
	st, router, _, webhook := discordRouter(t)
	slug := alertVendor(t, st)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		fmt.Sprintf(`{"discord":{"url":%q}}`, webhook), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a visitor with no account: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "subscribers") {
		t.Errorf("answer %q does not say who it is for", rec.Body.String())
	}
}

// A sentence is not an address and is refused at the door.
func TestADiscordWebhookMustBeAnAddress(t *testing.T) {
	st, router, _, _ := discordRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	for _, webhookURL := range []string{
		"discord.com/api/webhooks/100/token",
		"the alerts channel",
		"ftp://discord.com/api/webhooks/100/token",
	} {
		body := fmt.Sprintf(`{"discord":{"url":%q}}`, webhookURL)
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q = %d, want 400: %s", webhookURL, rec.Code, rec.Body.String())
		}
	}
}

// The saved URL comes back masked and survives a save of something else.
func TestSavingADiscordChannelAndReadingItBack(t *testing.T) {
	st, router, _, webhookURL := discordRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		fmt.Sprintf(`{"discord":{"url":%q},"wholeService":true,"components":[]}`, webhookURL), cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving a channel = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	state := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if state.Code != http.StatusOK {
		t.Fatalf("reading the alert = %d, want 200: %s", state.Code, state.Body.String())
	}
	var body struct {
		Subscribed    bool   `json:"subscribed"`
		Discord       string `json:"discord"`
		DiscordPaused bool   `json:"discordPaused"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Subscribed {
		t.Error("an alert with a channel on it reads as none")
	}
	if body.Discord != webhookURL {
		t.Errorf("discord = %q, want the URL that was saved", body.Discord)
	}
	if body.DiscordPaused {
		t.Error("a channel that was just saved reads as paused")
	}

	// A channel-only row has no address for the email field.
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
		if strings.Contains(strings.Join(row.Channels, ","), "discord") {
			found = true
			if row.Email != "" {
				t.Errorf("email = %q, want nothing for a channel with no address", row.Email)
			}
		}
	}
	if !found {
		t.Error("the alert list does not say the row arrives by Discord")
	}
}

// A channel is tried before it is saved, and Discord's answer is handed back.
func TestTryingADiscordChannelBeforeSavingIt(t *testing.T) {
	st, router, spy, webhook := discordRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/discord/test",
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
	if strings.Contains(sent, "Stop these alerts") {
		t.Errorf("a test carries a way out of something that was never saved:\n%s", sent)
	}
}

// A saved channel is tried with its saved URL when the field is empty.
func TestADiscordChannelIsTriedWithTheSavedURL(t *testing.T) {
	st, router, spy, webhook := discordRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	savedWithDiscord(t, st, user, id, webhook)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/discord/test", `{}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the saved URL: %s", rec.Code, rec.Body.String())
	}
	if sent := spy.sent(t); !strings.Contains(sent, "test message") {
		t.Errorf("what was sent is not a test:\n%s", sent)
	}
}

// A refusal's reason is what the page shows, not a failure of our own.
func TestARefusedDiscordIsSaidBack(t *testing.T) {
	st, router, spy, webhook := discordRouter(t)
	spy.refused(http.StatusBadRequest, `{"message":"Invalid Form Body"}`)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/discord/test",
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
	if !strings.Contains(body.Error, "Invalid Form Body") {
		t.Errorf("error = %q, want what Discord said", body.Error)
	}
}

// The channel has its own switch, and its link stops that channel.
func TestADiscordChannelCanBePaused(t *testing.T) {
	st, router, _, webhook := discordRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	token := savedWithDiscord(t, st, user, id, webhook)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/pause",
		`{"kind":"discord","paused":true}`, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pausing the channel = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	state := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	var body struct {
		DiscordPaused bool `json:"discordPaused"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.DiscordPaused {
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
	if page.Kind != store.ChannelDiscord {
		t.Errorf("kind = %q, want discord", page.Kind)
	}
	if page.Destination != "your Discord channel" {
		t.Errorf("destination = %q, want the channel masked", page.Destination)
	}
}
