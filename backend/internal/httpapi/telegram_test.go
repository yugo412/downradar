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

// chatSpy is a Bot API the test runs. It records what it was sent and answers the
// way the real one does, so a test can see what a chat would have received.
type chatSpy struct {
	mu       sync.Mutex
	paths    []string
	messages []string
	refusal  string
}

func (c *chatSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	c.mu.Lock()
	c.paths = append(c.paths, r.URL.Path)
	c.messages = append(c.messages, string(body))
	refusal := c.refusal
	c.mu.Unlock()

	answer := refusal
	if answer == "" {
		answer = `{"ok":true,"result":{"message_id":1}}`
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(answer))
}

// refuse makes the answers the ones Telegram gives for a chat it will not deliver
// to.
func (c *chatSpy) refuse(description string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refusal = fmt.Sprintf(`{"ok":false,"description":%q}`, description)
}

// sent is what one message carried: the path it went to and its body.
func (c *chatSpy) sent(t *testing.T) (string, string) {
	t.Helper()

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		t.Fatal("nothing was sent to the chat service")
	}
	return c.paths[len(c.paths)-1], c.messages[len(c.messages)-1]
}

// telegramRouter is the test router with the chat way turned on, pointed at a chat
// service the test runs.
func telegramRouter(t *testing.T) (*store.Store, http.Handler, *chatSpy) {
	t.Helper()

	spy := &chatSpy{}
	server := httptest.NewServer(spy)
	t.Cleanup(server.Close)

	st, router, _ := testRouterWithOptions(t, func(opts *Options) {
		opts.TelegramEnabled = true
		opts.Notifier = opts.Notifier.WithTelegram(server.URL)
	})
	return st, router, spy
}

// savedWithChat writes an alert for one subscriber with a chat and the bot that
// speaks to it, and returns the token a message would carry.
func savedWithChat(t *testing.T, st *store.Store, user *store.User, serviceID int64) string {
	t.Helper()

	token := fmt.Sprintf("token-chat-%d", time.Now().UnixNano())
	if err := st.SubscribeAlert(context.Background(), serviceID, &user.ID, store.AlertChange{
		Telegram:      &store.AlertTelegram{ChatID: "42", BotToken: "999:bot-secret"},
		TelegramToken: token,
		Targets:       store.WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })
	return token
}

// A chat is a way for somebody with an account: a visitor who is not signed in
// has no chat to name, the same way they have no webhook.
func TestAChatIsForSubscribers(t *testing.T) {
	st, router, _ := telegramRouter(t)
	slug := alertVendor(t, st)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"telegram":{"chatId":"42","botToken":"999:secret"}}`, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a visitor with no account: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "subscribers") {
		t.Errorf("answer %q does not say who it is for", rec.Body.String())
	}
}

// A chat that is not a chat at all is refused at the door, so a sentence is not
// stored as the place messages go.
func TestAChatMustLookLikeOne(t *testing.T) {
	st, router, _ := telegramRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	for _, chat := range []string{"the group chat", "@", "-"} {
		body := fmt.Sprintf(`{"telegram":{"chatId":%q,"botToken":"999:secret"}}`, chat)
		rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q = %d, want 400: %s", chat, rec.Code, rec.Body.String())
		}
	}
}

// The first save of a chat has to name the bot that speaks to it: a way that could
// never be sent to is refused rather than saved.
func TestAChatNeedsABotTokenWhenSaved(t *testing.T) {
	st, router, _ := telegramRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"telegram":{"chatId":"42"}}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("saving a chat with no bot = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bot token") {
		t.Errorf("answer %q does not say what is missing", rec.Body.String())
	}

	// A token that is not a token is refused as well, so a stray paste does not
	// become the bot of an alert.
	bad := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"telegram":{"chatId":"42","botToken":"hello there"}}`, cookie)
	if bad.Code != http.StatusBadRequest {
		t.Errorf("a token with a space = %d, want 400: %s", bad.Code, bad.Body.String())
	}
}

// A chat is saved with its bot and read back with the rest, and the token itself
// is never handed back to the page.
func TestSavingAChatAndReadingItBack(t *testing.T) {
	st, router, _ := telegramRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"telegram":{"chatId":"-1001234567890","botToken":"999:bot-secret"},"wholeService":true,"components":[]}`,
		cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving a chat = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	state := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if state.Code != http.StatusOK {
		t.Fatalf("reading the alert = %d, want 200: %s", state.Code, state.Body.String())
	}
	var body struct {
		Subscribed      bool   `json:"subscribed"`
		Telegram        string `json:"telegram"`
		TelegramPaused  bool   `json:"telegramPaused"`
		TelegramEnabled bool   `json:"telegramEnabled"`
		TelegramBot     string `json:"telegramBot"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Subscribed {
		t.Error("an alert with a chat on it reads as none")
	}
	if body.Telegram != "-1001234567890" {
		t.Errorf("chat = %q, want the one that was saved", body.Telegram)
	}
	if !body.TelegramEnabled {
		t.Error("the page is not told that a chat can be set up")
	}
	if body.TelegramBot != "999:bot-secret" {
		t.Errorf("bot = %q, want the one the owner saved, so the page can show it", body.TelegramBot)
	}
	if body.TelegramPaused {
		t.Error("a chat that was just saved reads as paused")
	}
}

// A chat is tried before it is saved, with a made up change, and what the chat
// service says is handed back as it is.
func TestTryingAChatBeforeSavingIt(t *testing.T) {
	st, router, spy := telegramRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/telegram/test",
		`{"chatId":"-1001234567890","botToken":"999:bot-secret"}`, cookie)
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

	path, sent := spy.sent(t)
	if !strings.HasSuffix(path, "/bot999:bot-secret/sendMessage") {
		t.Errorf("called %q, want the subscriber's own bot", path)
	}
	for _, want := range []string{"-1001234567890", "test message", "Major outage"} {
		if !strings.Contains(sent, want) {
			t.Errorf("what was sent does not contain %q:\n%s", want, sent)
		}
	}
}

// A chat that is already saved is tried with the bot it was saved with, because
// the page is never handed the token again.
func TestAChatIsTriedWithTheBotItWasSavedWith(t *testing.T) {
	st, router, spy := telegramRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	savedWithChat(t, st, user, id)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/telegram/test",
		`{"chatId":"42"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the saved bot: %s", rec.Code, rec.Body.String())
	}

	path, _ := spy.sent(t)
	if !strings.HasSuffix(path, "/bot999:bot-secret/sendMessage") {
		t.Errorf("called %q, want the bot the chat was saved with", path)
	}
}

// A chat service that will not deliver says why, and that is what the page shows
// rather than a failure of our own.
func TestARefusedChatIsSaidBack(t *testing.T) {
	st, router, spy := telegramRouter(t)
	spy.refuse("Bad Request: chat not found")
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/telegram/test",
		`{"chatId":"42","botToken":"999:bot-secret"}`, cookie)
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
	if !strings.Contains(body.Error, "chat not found") {
		t.Errorf("error = %q, want what the chat service said", body.Error)
	}
}

// With no sender loop running there is nothing to deliver a message, so the way is
// refused rather than taken.
func TestAChatIsRefusedWhenNothingSends(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"telegram":{"chatId":"42","botToken":"999:secret"}}`, cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}

	tried := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/telegram/test",
		`{"chatId":"42","botToken":"999:secret"}`, cookie)
	if tried.Code != http.StatusServiceUnavailable {
		t.Errorf("trying a chat = %d, want 503", tried.Code)
	}

	state := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	var body struct {
		TelegramEnabled bool `json:"telegramEnabled"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TelegramEnabled {
		t.Error("a chat is offered while nothing would send it")
	}
}

// Stopping a chat is always allowed: what is there should be able to be taken
// away.
func TestAChatCanAlwaysBeStopped(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	savedWithChat(t, st, user, id)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"telegram":{"chatId":""}}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("stopping a chat = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	// The chat was the only way it had, so there is nothing live left on it.
	if _, err := st.UserAlert(context.Background(), user.ID, id); err == nil {
		t.Error("an alert whose only way was stopped is still there")
	}
}

// A chat way can be paused and started again from the page, which is the switch
// at the foot of the panel.
func TestPausingAChat(t *testing.T) {
	st, router, _ := telegramRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts",
		`{"telegram":{"chatId":"42","botToken":"999:secret"},"wholeService":true,"components":[]}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving a chat = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	paused := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/pause",
		`{"kind":"telegram","paused":true}`, cookie)
	if paused.Code != http.StatusNoContent {
		t.Fatalf("pausing a chat = %d, want 204: %s", paused.Code, paused.Body.String())
	}

	state := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	var body struct {
		TelegramPaused bool `json:"telegramPaused"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.TelegramPaused {
		t.Error("a paused chat does not read as paused")
	}
}

// The link in a message is read through the channel's own token, and the page it
// opens says what it is about to stop.
func TestTheLinkInAMessageNamesTheChat(t *testing.T) {
	st, router, _ := telegramRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	token := savedWithChat(t, st, user, id)

	link := call(t, router, http.MethodGet, "/api/v1/alerts/"+token, "", "")
	if link.Code != http.StatusOK {
		t.Fatalf("reading the link = %d, want 200: %s", link.Code, link.Body.String())
	}

	var body struct {
		Kind        string `json:"kind"`
		Destination string `json:"destination"`
	}
	if err := json.Unmarshal(link.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Kind != "telegram" {
		t.Errorf("kind = %q, want telegram", body.Kind)
	}
	if body.Destination != "your chat" {
		t.Errorf("destination = %q, want the chat named rather than spelled out", body.Destination)
	}
}

// The link in a message can pause the way it came from and start it again, and
// the page that opens from it knows which state it is in.
func TestALinkCanPauseTheWayItCameFrom(t *testing.T) {
	st, router, _ := telegramRouter(t)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	id, _, err := st.PublicService(context.Background(), slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	token := savedWithChat(t, st, user, id)

	paused := call(t, router, http.MethodPatch, "/api/v1/alerts/"+token, `{"paused":true}`, "")
	if paused.Code != http.StatusNoContent {
		t.Fatalf("pausing by the link = %d, want 204: %s", paused.Code, paused.Body.String())
	}

	link := call(t, router, http.MethodGet, "/api/v1/alerts/"+token, "", "")
	var body struct {
		Paused bool `json:"paused"`
	}
	if err := json.Unmarshal(link.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Paused {
		t.Error("the way the link came from does not read as paused")
	}

	started := call(t, router, http.MethodPatch, "/api/v1/alerts/"+token, `{"paused":false}`, "")
	if started.Code != http.StatusNoContent {
		t.Fatalf("starting it again = %d, want 204: %s", started.Code, started.Body.String())
	}

	// A link that is not one pauses nothing.
	bad := call(t, router, http.MethodPatch, "/api/v1/alerts/not-a-token", `{"paused":true}`, "")
	if bad.Code != http.StatusNotFound {
		t.Errorf("an unknown link = %d, want 404: %s", bad.Code, bad.Body.String())
	}
}
