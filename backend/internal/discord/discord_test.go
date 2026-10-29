package discord

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizeURL(t *testing.T) {
	app := NewApp("client-id", "client-secret")

	raw := app.AuthorizeURL("the-state", "https://downradar.test/api/v1/discord/callback")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Host != "discord.com" || parsed.Path != "/oauth2/authorize" {
		t.Errorf("authorize url = %s, want Discord's oauth page", raw)
	}

	query := parsed.Query()
	for name, want := range map[string]string{
		"response_type": "code",
		"client_id":     "client-id",
		"scope":         "webhook.incoming",
		"state":         "the-state",
		"redirect_uri":  "https://downradar.test/api/v1/discord/callback",
	} {
		if got := query.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestExchange(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if contentType := r.Header.Get("Content-Type"); contentType != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want a form", contentType)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
			return
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"webhook":{"url":"https://discord.com/api/webhooks/100/token","name":"DownRadar","channel_id":"200","guild_id":"300"}}`)
	}))
	defer srv.Close()

	app := NewApp("client-id", "client-secret")
	app.tokenURL = srv.URL

	hook, err := app.Exchange(context.Background(), "the-code", "https://downradar.test/api/v1/discord/callback")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	for name, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "the-code",
		"redirect_uri":  "https://downradar.test/api/v1/discord/callback",
		"client_id":     "client-id",
		"client_secret": "client-secret",
	} {
		if got := gotForm.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	if hook.URL != "https://discord.com/api/webhooks/100/token" {
		t.Errorf("URL = %q", hook.URL)
	}
	if hook.Name != "DownRadar" || hook.Channel != "200" || hook.Guild != "300" {
		t.Errorf("hook = %+v, want the channel and server Discord named", hook)
	}
}

func TestExchangeRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"http status", http.StatusBadRequest, `{"error":"invalid_grant"}`, "invalid_grant"},
		{"error field", http.StatusOK, `{"error":"invalid_grant","error_description":"bad code"}`, "bad code"},
		{"no webhook", http.StatusOK, `{"webhook":{}}`, "carried no webhook"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			app := NewApp("client-id", "client-secret")
			app.tokenURL = srv.URL

			_, err := app.Exchange(context.Background(), "the-code", "https://downradar.test/callback")
			if err == nil {
				t.Fatal("Exchange accepted a refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}
