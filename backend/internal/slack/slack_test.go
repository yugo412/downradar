package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const testRedirect = "https://downradar.test/api/v1/slack/callback"

func TestAuthorizeURL(t *testing.T) {
	app := NewApp("the-client-id", "the-client-secret")

	parsed, err := url.Parse(app.AuthorizeURL("the-state", testRedirect))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Host != "slack.com" || parsed.Path != "/oauth/v2/authorize" {
		t.Errorf("authorize url = %s, want Slack's own page", parsed)
	}

	query := parsed.Query()
	if got := query.Get("client_id"); got != "the-client-id" {
		t.Errorf("client_id = %q", got)
	}
	if got := query.Get("redirect_uri"); got != testRedirect {
		t.Errorf("redirect_uri = %q", got)
	}
	if got := query.Get("state"); got != "the-state" {
		t.Errorf("state = %q", got)
	}
	if got := query.Get("scope"); got != "incoming-webhook" {
		t.Errorf("scope = %q, want the one that makes a channel webhook", got)
	}
}

func TestExchange(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		_, _ = w.Write([]byte(`{
			"ok": true,
			"team": {"name": "The Workspace"},
			"incoming_webhook": {
				"url": "https://hooks.slack.com/services/T0/B0/X",
				"channel": "#alerts"
			}
		}`))
	}))
	t.Cleanup(server.Close)

	app := NewApp("the-client-id", "the-client-secret")
	app.accessURL = server.URL

	hook, err := app.Exchange(context.Background(), "the-code", testRedirect)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if hook.URL != "https://hooks.slack.com/services/T0/B0/X" {
		t.Errorf("url = %q", hook.URL)
	}
	if hook.Channel != "#alerts" {
		t.Errorf("channel = %q", hook.Channel)
	}
	if hook.Team != "The Workspace" {
		t.Errorf("team = %q", hook.Team)
	}

	if got := form.Get("client_id"); got != "the-client-id" {
		t.Errorf("client_id = %q", got)
	}
	if got := form.Get("client_secret"); got != "the-client-secret" {
		t.Errorf("client_secret = %q", got)
	}
	if got := form.Get("code"); got != "the-code" {
		t.Errorf("code = %q", got)
	}
	if got := form.Get("redirect_uri"); got != testRedirect {
		t.Errorf("redirect_uri = %q", got)
	}
}

func TestExchangeRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok": false, "error": "invalid_code"}`))
	}))
	t.Cleanup(server.Close)

	app := NewApp("the-client-id", "the-client-secret")
	app.accessURL = server.URL

	if _, err := app.Exchange(context.Background(), "the-code", testRedirect); err == nil {
		t.Fatal("a refusal was taken for an install")
	}
}

func TestExchangeWithoutAWebhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok": true, "team": {"name": "The Workspace"}}`))
	}))
	t.Cleanup(server.Close)

	app := NewApp("the-client-id", "the-client-secret")
	app.accessURL = server.URL

	if _, err := app.Exchange(context.Background(), "the-code", testRedirect); err == nil {
		t.Fatal("an answer without a webhook was taken for an install")
	}
}
