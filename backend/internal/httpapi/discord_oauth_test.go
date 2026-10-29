package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/discord"
	"downradar.info/backend/internal/store"
)

// fakeDiscord stands in for the site's Discord app. The two calls the handlers
// make are the only parts that leave the site, so a test decides what they say.
type fakeDiscord struct {
	hook        discord.Webhook
	err         error
	code        string
	redirectURI string
}

func (f *fakeDiscord) AuthorizeURL(state, redirectURI string) string {
	f.redirectURI = redirectURI
	return "https://discord.invalid/authorize?state=" + url.QueryEscape(state) +
		"&redirect_uri=" + url.QueryEscape(redirectURI)
}

func (f *fakeDiscord) Exchange(_ context.Context, code, redirectURI string) (discord.Webhook, error) {
	f.code = code
	f.redirectURI = redirectURI
	if f.err != nil {
		return discord.Webhook{}, f.err
	}
	return f.hook, nil
}

func discordInstallRouter(t *testing.T, app DiscordApp) (*store.Store, http.Handler) {
	t.Helper()

	st, router, _ := testRouterWithOptions(t, func(opts *Options) {
		opts.Discord = app
		opts.PublicURL = "https://downradar.test"
	})
	return st, router
}

// newDiscordWebhook is the channel an install lands on. It answers the way
// Discord does, so the welcome message has somewhere to go.
func newDiscordWebhook(t *testing.T) (*discordSpy, string) {
	t.Helper()

	spy := &discordSpy{}
	server := httptest.NewServer(spy)
	t.Cleanup(server.Close)
	return spy, server.URL + "/api/webhooks/100/token"
}

// startDiscordInstall follows the first half of the flow and hands back what the
// browser would be holding: the state in Discord's address and the cookie behind
// it.
func startDiscordInstall(t *testing.T, router http.Handler, slug, cookie, query string) (state, flowCookie string) {
	t.Helper()

	path := "/api/v1/vendors/" + slug + "/alerts/discord/connect"
	if query != "" {
		path += "?" + query
	}
	rec := call(t, router, http.MethodGet, path, "", cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("start = %d, want a redirect: %s", rec.Code, rec.Body.String())
	}

	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse the Discord url: %v", err)
	}
	if location.Host != "discord.invalid" {
		t.Fatalf("start sent the browser to %s, not Discord", location)
	}
	state = location.Query().Get("state")
	if state == "" {
		t.Fatal("no state in Discord's address")
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == discordStateCookie {
			flowCookie = c.Name + "=" + c.Value
		}
	}
	if flowCookie == "" {
		t.Fatal("no flow cookie was set")
	}
	return state, flowCookie
}

func finishDiscordInstall(t *testing.T, router http.Handler, state, cookie, extra string) *http.Response {
	t.Helper()

	path := "/api/v1/discord/callback?state=" + url.QueryEscape(state)
	if extra != "" {
		path += "&" + extra
	}
	return call(t, router, http.MethodGet, path, "", cookie).Result()
}

type discordStateAnswer struct {
	Subscribed     bool     `json:"subscribed"`
	Discord        string   `json:"discord"`
	DiscordChannel string   `json:"discordChannel"`
	DiscordEnabled bool     `json:"discordEnabled"`
	WholeService   bool     `json:"wholeService"`
	Components     []string `json:"components"`
}

func readDiscordState(t *testing.T, router http.Handler, slug, cookie string) discordStateAnswer {
	t.Helper()

	rec := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading the alert = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var answer discordStateAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return answer
}

// One click writes the webhook the install came back with, and the page can tell
// it apart from a pasted URL by the channel the install named.
func TestConnectingDiscordSavesTheChannel(t *testing.T) {
	spy, webhook := newDiscordWebhook(t)
	app := &fakeDiscord{hook: discord.Webhook{
		URL:     webhook,
		Channel: "200",
		Guild:   "300",
	}}
	st, router := discordInstallRouter(t, app)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	state, flowCookie := startDiscordInstall(t, router, slug, cookie, "wholeService=true")
	if got := app.redirectURI; got != "https://downradar.test/api/v1/discord/callback" {
		t.Errorf("redirect_uri = %q, want the registered address", got)
	}

	res := finishDiscordInstall(t, router, state, flowCookie, "code=the-code")
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if location.Path != "/alerts/"+slug || location.Query().Get("discord") != "connected" {
		t.Errorf("after connecting = %q, want the alert page saying connected",
			res.Header.Get("Location"))
	}
	if app.code != "the-code" || app.redirectURI != "https://downradar.test/api/v1/discord/callback" {
		t.Errorf("exchange used code %q and redirect %q", app.code, app.redirectURI)
	}

	answer := readDiscordState(t, router, slug, cookie)
	if !answer.Subscribed {
		t.Fatal("an alert with a channel on it reads as none")
	}
	if answer.Discord != app.hook.URL {
		t.Errorf("discord = %q, want the URL the install made", answer.Discord)
	}
	if answer.DiscordChannel != "200" {
		t.Errorf("discordChannel = %q, want the channel the install made", answer.DiscordChannel)
	}
	if !answer.DiscordEnabled {
		t.Error("a site with a Discord app says it has none")
	}
	if !answer.WholeService {
		t.Error("the alert was written for something other than the whole service")
	}

	// The cookie is spent by the first callback, whatever the answer is.
	spent := false
	for _, c := range res.Cookies() {
		if c.Name == discordStateCookie && c.MaxAge < 0 {
			spent = true
		}
	}
	if !spent {
		t.Error("the flow cookie was not cleared")
	}

	// The channel hears about the install straight away, not only at the first
	// change.
	if sent := spy.sent(t); !strings.Contains(sent, "connected") {
		t.Errorf("the channel did not get the welcome message:\n%s", sent)
	}
}

// What the page had ticked travels with the install, so the alert it writes
// reports on the same things the page was showing.
func TestConnectingDiscordKeepsThePickedComponents(t *testing.T) {
	_, webhook := newDiscordWebhook(t)
	app := &fakeDiscord{hook: discord.Webhook{
		URL:     webhook,
		Channel: "200",
	}}
	st, router := discordInstallRouter(t, app)
	slug, sourceIDs := alertVendorWithComponents(t, st, 3)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	query := url.Values{
		"wholeService": {"false"},
		"components":   {sourceIDs[0], sourceIDs[2]},
	}.Encode()
	state, flowCookie := startDiscordInstall(t, router, slug, cookie, query)
	finishDiscordInstall(t, router, state, flowCookie, "code=the-code")

	answer := readDiscordState(t, router, slug, cookie)
	if answer.WholeService {
		t.Error("the alert reports on the whole service instead of the picked components")
	}
	if len(answer.Components) != 2 || answer.Components[0] != sourceIDs[0] || answer.Components[1] != sourceIDs[2] {
		t.Errorf("components = %v, want the two the page picked", answer.Components)
	}
}

func TestDiscordCallbackRejectsAnotherBrowser(t *testing.T) {
	_, webhook := newDiscordWebhook(t)
	app := &fakeDiscord{hook: discord.Webhook{URL: webhook, Channel: "200"}}
	st, router := discordInstallRouter(t, app)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)
	state, _ := startDiscordInstall(t, router, slug, cookie, "wholeService=true")

	res := finishDiscordInstall(t, router, state, "", "code=the-code")
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if location.Path != "/alerts" || location.Query().Get("discord") != "failed" {
		t.Errorf("after a callback without the flow cookie = %q, want a failure",
			res.Header.Get("Location"))
	}
}

func TestDiscordInstallIsUnavailableWithoutTheApp(t *testing.T) {
	st, router := discordInstallRouter(t, nil)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	rec := call(t, router, http.MethodGet,
		"/api/v1/vendors/"+slug+"/alerts/discord/connect?wholeService=true", "", cookie)
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if location.Path != "/alerts/"+slug || location.Query().Get("discord") != "unavailable" {
		t.Errorf("after an unavailable install = %q, want the alert page saying unavailable",
			rec.Header().Get("Location"))
	}
}

func TestDiscordInstallCarriesTheReasonBack(t *testing.T) {
	_, webhook := newDiscordWebhook(t)
	app := &fakeDiscord{hook: discord.Webhook{URL: webhook, Channel: "200"}}
	st, router := discordInstallRouter(t, app)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)
	state, flowCookie := startDiscordInstall(t, router, slug, cookie, "wholeService=true")

	res := finishDiscordInstall(t, router, state, flowCookie, "error=access_denied")
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if location.Path != "/alerts/"+slug || location.Query().Get("discord") != "refused" {
		t.Errorf("after a refusal = %q, want the alert page saying refused",
			res.Header.Get("Location"))
	}
}

func TestDiscordInstallExchangeFailure(t *testing.T) {
	app := &fakeDiscord{err: fmt.Errorf("discord said no")}
	st, router := discordInstallRouter(t, app)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)
	state, flowCookie := startDiscordInstall(t, router, slug, cookie, "wholeService=true")

	res := finishDiscordInstall(t, router, state, flowCookie, "code=the-code")
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if location.Path != "/alerts/"+slug || location.Query().Get("discord") != "failed" {
		t.Errorf("after a failed exchange = %q, want the alert page saying failed",
			res.Header.Get("Location"))
	}
}
