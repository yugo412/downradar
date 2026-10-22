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
	"downradar.info/backend/internal/slack"
	"downradar.info/backend/internal/store"
)

// fakeSlack stands in for the site's Slack app. The two calls the handlers make
// are the only parts that leave the site, so a test decides what they say.
type fakeSlack struct {
	hook        slack.Webhook
	err         error
	code        string
	redirectURI string
}

func (f *fakeSlack) AuthorizeURL(state, redirectURI string) string {
	f.redirectURI = redirectURI
	return "https://slack.invalid/authorize?state=" + url.QueryEscape(state) +
		"&redirect_uri=" + url.QueryEscape(redirectURI)
}

func (f *fakeSlack) Exchange(_ context.Context, code, redirectURI string) (slack.Webhook, error) {
	f.code = code
	f.redirectURI = redirectURI
	if f.err != nil {
		return slack.Webhook{}, f.err
	}
	return f.hook, nil
}

func slackInstallRouter(t *testing.T, app SlackApp) (*store.Store, http.Handler) {
	t.Helper()

	st, router, _ := testRouterWithOptions(t, func(opts *Options) {
		opts.Slack = app
		opts.PublicURL = "https://downradar.test"
	})
	return st, router
}

// newSlackWebhook is the channel an install lands on. It answers the way Slack
// does, so the message sent the moment the install finishes has somewhere to go.
func newSlackWebhook(t *testing.T) (*slackSpy, string) {
	t.Helper()

	spy := &slackSpy{}
	server := httptest.NewServer(spy)
	t.Cleanup(server.Close)
	return spy, server.URL + "/services/T0/B0/X"
}

// startSlackInstall follows the first half of the flow and hands back what the
// browser would be holding: the state in Slack's address and the cookie behind
// it.
func startSlackInstall(t *testing.T, router http.Handler, slug, cookie, query string) (state, flowCookie string) {
	t.Helper()

	path := "/api/v1/vendors/" + slug + "/alerts/slack/connect"
	if query != "" {
		path += "?" + query
	}
	rec := call(t, router, http.MethodGet, path, "", cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("start = %d, want a redirect: %s", rec.Code, rec.Body.String())
	}

	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse the Slack url: %v", err)
	}
	if location.Host != "slack.invalid" {
		t.Fatalf("start sent the browser to %s, not Slack", location)
	}
	state = location.Query().Get("state")
	if state == "" {
		t.Fatal("no state in Slack's address")
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == slackStateCookie {
			flowCookie = c.Name + "=" + c.Value
		}
	}
	if flowCookie == "" {
		t.Fatal("no flow cookie was set")
	}
	return state, flowCookie
}

func finishSlackInstall(t *testing.T, router http.Handler, state, cookie, extra string) *http.Response {
	t.Helper()

	path := "/api/v1/slack/callback?state=" + url.QueryEscape(state)
	if extra != "" {
		path += "&" + extra
	}
	return call(t, router, http.MethodGet, path, "", cookie).Result()
}

type slackStateAnswer struct {
	Subscribed   bool     `json:"subscribed"`
	Slack        string   `json:"slack"`
	SlackChannel string   `json:"slackChannel"`
	SlackEnabled bool     `json:"slackEnabled"`
	WholeService bool     `json:"wholeService"`
	Components   []string `json:"components"`
}

func readSlackState(t *testing.T, router http.Handler, slug, cookie string) slackStateAnswer {
	t.Helper()

	rec := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug+"/alerts", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading the alert = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var answer slackStateAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return answer
}

// One click writes the channel the install came back with, and the page can say
// which one it is.
func TestConnectingSlackSavesTheChannel(t *testing.T) {
	spy, webhook := newSlackWebhook(t)
	app := &fakeSlack{hook: slack.Webhook{
		URL:     webhook,
		Channel: "#alerts",
		Team:    "The Workspace",
	}}
	st, router := slackInstallRouter(t, app)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	state, flowCookie := startSlackInstall(t, router, slug, cookie, "wholeService=true")
	if got := app.redirectURI; got != "https://downradar.test/api/v1/slack/callback" {
		t.Errorf("redirect_uri = %q, want the registered address", got)
	}

	res := finishSlackInstall(t, router, state, flowCookie, "code=the-code")
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if location.Path != "/alerts/"+slug || location.Query().Get("slack") != "connected" {
		t.Errorf("after connecting = %q, want the alert page saying connected",
			res.Header.Get("Location"))
	}
	if app.code != "the-code" || app.redirectURI != "https://downradar.test/api/v1/slack/callback" {
		t.Errorf("exchange used code %q and redirect %q", app.code, app.redirectURI)
	}

	answer := readSlackState(t, router, slug, cookie)
	if !answer.Subscribed {
		t.Fatal("an alert with a channel on it reads as none")
	}
	if answer.Slack != app.hook.URL {
		t.Errorf("slack = %q, want the URL the install made", answer.Slack)
	}
	if answer.SlackChannel != "#alerts" {
		t.Errorf("slackChannel = %q, want the channel the install made", answer.SlackChannel)
	}
	if !answer.SlackEnabled {
		t.Error("a site with a Slack app says it has none")
	}
	if !answer.WholeService {
		t.Error("the alert was written for something other than the whole service")
	}

	// The cookie is spent by the first callback, whatever the answer is.
	spent := false
	for _, c := range res.Cookies() {
		if c.Name == slackStateCookie && c.MaxAge < 0 {
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
func TestConnectingSlackKeepsThePickedComponents(t *testing.T) {
	_, webhook := newSlackWebhook(t)
	app := &fakeSlack{hook: slack.Webhook{
		URL:     webhook,
		Channel: "#alerts",
	}}
	st, router := slackInstallRouter(t, app)
	slug, sourceIDs := alertVendorWithComponents(t, st, 3)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	query := url.Values{
		"wholeService": {"false"},
		"components":   {sourceIDs[0], sourceIDs[2]},
	}.Encode()
	state, flowCookie := startSlackInstall(t, router, slug, cookie, query)

	res := finishSlackInstall(t, router, state, flowCookie, "code=the-code")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("callback = %d, want a redirect", res.StatusCode)
	}

	answer := readSlackState(t, router, slug, cookie)
	if answer.WholeService {
		t.Error("the alert watches the whole service, though two components were picked")
	}
	if len(answer.Components) != 2 {
		t.Fatalf("components = %v, want the two that were picked", answer.Components)
	}
	for _, want := range []string{sourceIDs[0], sourceIDs[2]} {
		found := false
		for _, got := range answer.Components {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("components = %v, want %q among them", answer.Components, want)
		}
	}
}

// A save from the page carries the URL but not the channel's name, so a save of
// something else keeps the name the install left. A different URL is a
// different channel and has none.
func TestAManualSaveKeepsTheConnectedChannelName(t *testing.T) {
	_, connected := newSlackWebhook(t)
	other := "https://hooks.slack.com/services/T0/B0/Y"

	app := &fakeSlack{hook: slack.Webhook{URL: connected, Channel: "#alerts"}}
	st, router := slackInstallRouter(t, app)
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)
	state, flowCookie := startSlackInstall(t, router, slug, cookie, "wholeService=true")
	finishSlackInstall(t, router, state, flowCookie, "code=the-code")

	body := fmt.Sprintf(`{"slack":{"url":%q},"wholeService":true}`, connected)
	if rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("saving the same URL = %d: %s", rec.Code, rec.Body.String())
	}
	if answer := readSlackState(t, router, slug, cookie); answer.SlackChannel != "#alerts" {
		t.Errorf("slackChannel = %q after an unrelated save, want the connected name",
			answer.SlackChannel)
	}

	body = fmt.Sprintf(`{"slack":{"url":%q},"wholeService":true}`, other)
	if rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts", body, cookie); rec.Code != http.StatusCreated {
		t.Fatalf("saving another URL = %d: %s", rec.Code, rec.Body.String())
	}
	if answer := readSlackState(t, router, slug, cookie); answer.SlackChannel != "" {
		t.Errorf("slackChannel = %q after the URL changed, want none", answer.SlackChannel)
	}
}

// Slack sends the browser back with a refusal when the person presses cancel,
// which is not an error to log as one.
func TestRefusingTheSlackInstall(t *testing.T) {
	st, router := slackInstallRouter(t, &fakeSlack{})
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)
	state, flowCookie := startSlackInstall(t, router, slug, cookie, "wholeService=true")

	res := finishSlackInstall(t, router, state, flowCookie, "error=access_denied")
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if location.Path != "/alerts/"+slug || location.Query().Get("slack") != "refused" {
		t.Errorf("after a refusal = %q, want the alert page saying so",
			res.Header.Get("Location"))
	}
	if answer := readSlackState(t, router, slug, cookie); answer.Subscribed {
		t.Error("a refusal wrote an alert")
	}
}

// A callback with no flow behind it did not start in this browser, so it must not
// touch an alert.
func TestACallbackWithoutItsStateChangesNothing(t *testing.T) {
	st, router := slackInstallRouter(t, &fakeSlack{})
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	res := finishSlackInstall(t, router, "made-up", "", "code=the-code")
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if location.Path != "/alerts" || location.Query().Get("slack") != "failed" {
		t.Errorf("callback = %q, want the alerts page saying it failed",
			res.Header.Get("Location"))
	}
	if answer := readSlackState(t, router, slug, cookie); answer.Subscribed {
		t.Error("a callback with no state wrote an alert")
	}
}

// The one click path needs a session, like every other way that belongs to an
// account.
func TestConnectingSlackNeedsASession(t *testing.T) {
	st, router := slackInstallRouter(t, &fakeSlack{})
	slug := alertVendor(t, st)

	rec := call(t, router, http.MethodGet,
		"/api/v1/vendors/"+slug+"/alerts/slack/connect?wholeService=true", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("connect = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// Without the site's app there is no button to press, and the page says why.
func TestSlackInstallWithoutAnApp(t *testing.T) {
	st, router, _ := testRouterWithOptions(t, func(opts *Options) {
		opts.PublicURL = "https://downradar.test"
	})
	slug := alertVendor(t, st)

	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	if answer := readSlackState(t, router, slug, cookie); answer.SlackEnabled {
		t.Error("a site without a Slack app says it has one")
	}

	rec := call(t, router, http.MethodGet,
		"/api/v1/vendors/"+slug+"/alerts/slack/connect?wholeService=true", "", cookie)
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if location.Path != "/alerts/"+slug || location.Query().Get("slack") != "unavailable" {
		t.Errorf("connect = %q, want the alert page saying it is not set up",
			rec.Header().Get("Location"))
	}
}
